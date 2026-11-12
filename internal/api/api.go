// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Identity tells who made the request.
type Identity func(r *http.Request) (auth.Token, bool)

// Handler serves /api/… (JSON). Like MCP it offers no delete operations;
// those stay in the web UI behind a confirmation.
type Handler struct {
	Lib      *core.Library
	Identity Identity
}

// Routes returns the API mux.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.Handle("GET /api/repos", h.need(auth.Read, h.list))
	mux.Handle("GET /api/repos/{id...}", h.need(auth.Read, h.get))
	mux.Handle("POST /api/repos", h.need(auth.Write, h.add))
	mux.Handle("POST /api/sync/{id...}", h.need(auth.Write, h.sync))
	mux.Handle("GET /api/tags", h.need(auth.Read, h.tags))
	mux.Handle("GET /api/collections", h.need(auth.Read, h.collections))
	mux.Handle("GET /api/jobs", h.need(auth.Read, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, h.Lib.Jobs()) }))
	return mux
}

// fromOtherSite reports a state-changing request a web page of another
// site made in the user's browser (R-012): browsers name the page in
// Origin and Sec-Fetch-Site, and such "simple" requests cannot carry JSON.
func fromOtherSite(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return ""
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			return "Anfrage von fremder Seite abgelehnt"
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return "Anfrage von fremder Seite abgelehnt"
	}
	if r.ContentLength != 0 {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
			return "Content-Type muss application/json sein"
		}
	}
	return ""
}

func (h *Handler) need(right string, f http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if msg := fromOtherSite(r); msg != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
			return
		}
		tok, authed := h.Identity(r)
		if !tok.Has(right) {
			code := http.StatusForbidden
			if !authed {
				code = http.StatusUnauthorized
				w.Header().Set("WWW-Authenticate", `Bearer realm="RepoShelf"`)
			}
			writeJSON(w, code, map[string]string{"error": "Berechtigung fehlt: " + right})
			return
		}
		f(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, core.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrExists):
		code = http.StatusConflict
	case errors.Is(err, core.ErrInvalid):
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, err := h.Lib.List(r.Context(), store.Query{Text: q.Get("q"), Tag: q.Get("tag"), Kind: q.Get("kind"),
		Status: q.Get("status"), Collection: q.Get("collection"), Sort: q.Get("sort"), Limit: limit})
	if err != nil {
		fail(w, err)
		return
	}
	if entries == nil {
		entries = []store.Entry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	repo, err := h.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo.R(), "status": repo.Status(), "upstream": repo.Upstream, "backlinks": repo.Backlinks})
}

type addReq struct {
	URL      string   `json:"url"`
	Kind     string   `json:"kind"`
	Mode     string   `json:"mode"`
	Interval string   `json:"interval"`
	Tags     []string `json:"tags"`
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	var in addReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		fail(w, errors.Join(core.ErrInvalid, err))
		return
	}
	req := core.AddRequest{URL: in.URL, Kind: repometa.Kind(in.Kind), Mode: repometa.SyncMode(in.Mode), Tags: in.Tags, Actor: "api"}
	if in.Interval != "" {
		d, err := repometa.ParseDuration(in.Interval)
		if err != nil {
			fail(w, errors.Join(core.ErrInvalid, err))
			return
		}
		req.Interval = &d
	}
	repo, err := h.Lib.Add(r.Context(), req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": repo.R().ID, "key": repo.Entry.Key})
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	repo, err := h.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	queued := h.Lib.Enqueue(repo.R().ID, repo.Entry.Key, "api")
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": queued})
}

func (h *Handler) tags(w http.ResponseWriter, r *http.Request) {
	t, err := h.Lib.TagTree(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) collections(w http.ResponseWriter, r *http.Request) {
	c, err := h.Lib.Collections(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
