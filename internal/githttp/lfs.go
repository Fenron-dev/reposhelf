// SPDX-License-Identifier: GPL-3.0-or-later

package githttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Git LFS over HTTP (T-9.18): the batch API and basic transfers, so a
// clone through RepoShelf gets the real files (models, assets) instead of
// pointers. Objects live where git-lfs keeps them in the bare repository
// (lfs/objects/…); their name is the SHA-256 of their content.
// https://github.com/git-lfs/git-lfs/blob/main/docs/api/batch.md

const (
	lfsMediaType = "application/vnd.git-lfs+json"
	// maxBatch bounds the objects of one batch request.
	maxBatch = 1000
	// linkExpiry is how long a download/upload link is announced as valid.
	linkExpiry = 3600
)

type lfsObject struct {
	OID     string               `json:"oid"`
	Size    int64                `json:"size"`
	Auth    bool                 `json:"authenticated,omitempty"`
	Actions map[string]lfsAction `json:"actions,omitempty"`
	Error   *lfsError            `json:"error,omitempty"`
}

type lfsAction struct {
	Href      string            `json:"href"`
	Header    map[string]string `json:"header,omitempty"`
	ExpiresIn int               `json:"expires_in"`
}

type lfsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// lfs serves tail = /info/lfs/… of repo.
func (h *Handler) lfs(w http.ResponseWriter, r *http.Request, key string, repo *core.Repo, tail string) {
	rest := strings.TrimPrefix(tail, "/info/lfs/")
	upload := r.Method == http.MethodPut || (rest == "objects/batch" && r.Method == http.MethodPost && h.batchIsUpload(r))
	need := auth.Read
	if upload || strings.HasPrefix(rest, "locks") {
		need = auth.Write
	}
	if !h.allowed(w, r, need) {
		return
	}
	if need == auth.Write && repo.R().Kind == repometa.KindMirror {
		lfsFail(w, http.StatusForbidden, "Spiegel sind schreibgeschützt.")
		return
	}
	bare := repo.Paths.Mirror
	switch {
	case rest == "objects/batch" && r.Method == http.MethodPost:
		h.batch(w, r, key, bare)
	case strings.HasPrefix(rest, "objects/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		oid := strings.TrimPrefix(rest, "objects/")
		if !gitcli.ValidOID(oid) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(gitcli.LFSObjectPath(bare, oid))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", st.ModTime(), f)
	case strings.HasPrefix(rest, "objects/") && r.Method == http.MethodPut:
		oid := strings.TrimPrefix(rest, "objects/")
		err := gitcli.PutLFS(bare, oid, r.ContentLength, r.Body)
		switch {
		case errors.Is(err, gitcli.ErrLFSMismatch):
			lfsFail(w, http.StatusUnprocessableEntity, "Inhalt passt nicht zur Prüfsumme")
		case err != nil:
			h.Log.Warn("lfs upload", "oid", oid, "err", err)
			lfsFail(w, http.StatusBadRequest, "Upload fehlgeschlagen")
		default:
			w.WriteHeader(http.StatusOK)
		}
	case rest == "locks/verify" && r.Method == http.MethodPost:
		// No locking: say so politely, so git lfs push does not warn.
		writeLFS(w, http.StatusOK, map[string]any{"ours": []any{}, "theirs": []any{}})
	default:
		lfsFail(w, http.StatusNotFound, "nicht unterstützt")
	}
}

// batchIsUpload peeks at the operation without consuming the body.
func (h *Handler) batchIsUpload(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	body, err := readLimited(r, 4<<20)
	if err != nil {
		return false
	}
	var req struct {
		Operation string `json:"operation"`
	}
	_ = json.Unmarshal(body, &req)
	return req.Operation == "upload"
}

func (h *Handler) batch(w http.ResponseWriter, r *http.Request, key, bare string) {
	body, err := readLimited(r, 4<<20)
	if err != nil {
		lfsFail(w, http.StatusBadRequest, "Anfrage zu groß")
		return
	}
	var req struct {
		Operation string      `json:"operation"`
		Transfers []string    `json:"transfers"`
		Objects   []lfsObject `json:"objects"`
	}
	if err := json.Unmarshal(body, &req); err != nil || (req.Operation != "download" && req.Operation != "upload") || len(req.Objects) > maxBatch {
		lfsFail(w, http.StatusUnprocessableEntity, "ungültige Batch-Anfrage")
		return
	}
	base := baseURL(r) + Prefix + key + ".git/info/lfs/objects/"
	header := map[string]string{}
	if a := r.Header.Get("Authorization"); a != "" {
		header["Authorization"] = a // the client reuses what it sent us
	}
	out := make([]lfsObject, 0, len(req.Objects))
	for _, o := range req.Objects {
		res := lfsObject{OID: o.OID, Size: o.Size}
		if !gitcli.ValidOID(o.OID) || o.Size < 0 {
			res.Error = &lfsError{Code: http.StatusUnprocessableEntity, Message: "ungültiges Objekt"}
			out = append(out, res)
			continue
		}
		st, err := os.Stat(gitcli.LFSObjectPath(bare, o.OID))
		switch req.Operation {
		case "download":
			if err != nil {
				res.Error = &lfsError{Code: http.StatusNotFound, Message: "Objekt ist in RepoShelf nicht vorhanden"}
			} else {
				res.Size, res.Auth = st.Size(), true
				res.Actions = map[string]lfsAction{"download": {Href: base + o.OID, Header: header, ExpiresIn: linkExpiry}}
			}
		case "upload":
			if err != nil { // present objects need no upload
				res.Auth = true
				res.Actions = map[string]lfsAction{"upload": {Href: base + o.OID, Header: header, ExpiresIn: linkExpiry}}
			}
		}
		out = append(out, res)
	}
	writeLFS(w, http.StatusOK, map[string]any{"transfer": "basic", "objects": out, "hash_algo": "sha256"})
}

func writeLFS(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", lfsMediaType)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func lfsFail(w http.ResponseWriter, code int, msg string) {
	writeLFS(w, code, map[string]string{"message": msg})
}

// baseURL is how the client reached us (scheme and host of the request).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// readLimited reads the body once and puts it back for later readers.
func readLimited(r *http.Request, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("request too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}
