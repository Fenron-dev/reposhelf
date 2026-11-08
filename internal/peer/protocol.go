// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// maxBody bounds request bodies between instances.
const maxBody = 4 << 20

func (m *Manager) postJSON(ctx context.Context, url string, in, out any, signed bool, _ Peer) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := m.do(ctx, http.MethodPost, url, b, signed)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (m *Manager) getJSON(ctx context.Context, p Peer, path string, out any) error {
	resp, err := m.do(ctx, http.MethodGet, p.URL+path, nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

// Info is what a peer tells about itself.
type Info struct {
	Name         string   `json:"name"`
	Fingerprint  string   `json:"fingerprint"`
	Version      string   `json:"version"`
	Runner       bool     `json:"runner"`
	Capabilities []string `json:"capabilities"`
}

// RepoPayload is one repository for sync.
type RepoPayload struct {
	ID       string               `json:"id"`
	Key      string               `json:"key"`
	RepoYAML string               `json:"repo_yaml"`
	Upstream core.Upstream        `json:"upstream"`
	Notes    string               `json:"notes"`
	Releases []core.StoredRelease `json:"releases,omitempty"`
	Capsules []CapsulePayload     `json:"capsules,omitempty"`
}

// CapsulePayload carries a capsule manifest and its file list.
type CapsulePayload struct {
	Manifest artifact.Capsule `json:"manifest"`
	Files    []artifact.File  `json:"files"`
}

type reposRequest struct {
	Selections []Selection `json:"selections"`
}

type reposResponse struct {
	Repos []RepoPayload `json:"repos"`
	Token string        `json:"token"` // temporary read token for git and files
}

// Handler serves /peer/… for paired instances. Every endpoint except
// pairing requires a valid signature.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /peer/pair", func(w http.ResponseWriter, r *http.Request) {
		var req pairRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := m.acceptPair(req)
		if err != nil {
			http.Error(w, "Kopplungscode falsch oder abgelaufen", http.StatusForbidden)
			return
		}
		writeJSON(w, resp)
	})
	mux.HandleFunc("GET /peer/info", m.signed(func(w http.ResponseWriter, r *http.Request, p Peer, _ []byte) {
		cfg := m.Lib.Config()
		writeJSON(w, Info{Name: cfg.Instance.Name, Fingerprint: m.OwnFingerprint(), Runner: cfg.Runner.Enabled, Capabilities: cfg.Runner.Capabilities})
	}))
	mux.HandleFunc("POST /peer/repos", m.signed(func(w http.ResponseWriter, r *http.Request, p Peer, body []byte) {
		var req reposRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		repos, err := m.collect(r.Context(), req.Selections)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, reposResponse{Repos: repos, Token: m.issueTemp(p.ID)})
	}))
	mux.HandleFunc("GET /peer/asset", m.signed(func(w http.ResponseWriter, r *http.Request, p Peer, _ []byte) {
		q := r.URL.Query()
		path, err := m.Lib.AssetPath(r.Context(), q.Get("repo"), q.Get("tag"), q.Get("name"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	}))
	mux.HandleFunc("GET /peer/blob/{hash}", m.signed(func(w http.ResponseWriter, r *http.Request, p Peer, _ []byte) {
		h := r.PathValue("hash")
		if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
			http.NotFound(w, r)
			return
		}
		path := artifact.BlobsOf(m.Lib.Layout.Root).Path(h)
		if _, err := os.Stat(path); err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, path)
	}))
	mux.HandleFunc("POST /peer/builds", m.signed(m.acceptBuild))
	mux.HandleFunc("GET /peer/builds/{id}", m.signed(m.buildStatus))
	return mux
}

type signedHandler func(w http.ResponseWriter, r *http.Request, p Peer, body []byte)

func (m *Manager) signed(h signedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := m.verify(r, body)
		if err != nil {
			http.Error(w, "nicht gekoppelt oder Signatur ungültig", http.StatusUnauthorized)
			return
		}
		h(w, r, p, body)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// collect builds the payload for the selected repositories.
func (m *Manager) collect(ctx context.Context, sels []Selection) ([]RepoPayload, error) {
	want := map[string]string{} // repo ID → highest level
	all, err := m.Lib.List(ctx, store.Query{})
	if err != nil {
		return nil, err
	}
	raise := func(id, level string) {
		if levelIndex(level) > levelIndex(want[id]) || want[id] == "" {
			want[id] = level
		}
	}
	for _, s := range sels {
		switch s.Type {
		case "all":
			for _, e := range all {
				raise(e.ID, s.Level)
			}
		case "collection":
			members, err := m.Lib.CollectionMembers(ctx, s.Ref)
			if err != nil {
				continue
			}
			for _, e := range members {
				raise(e.ID, s.Level)
			}
		case "repo":
			if e, err := m.Lib.Store.Get(ctx, s.Ref); err == nil {
				raise(e.ID, s.Level)
			}
		}
	}
	var out []RepoPayload
	for id, level := range want {
		r, err := m.Lib.Get(ctx, id)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(r.Paths.RepoFile)
		if err != nil {
			continue
		}
		notes, _ := m.Lib.Notes(ctx, id)
		pl := RepoPayload{ID: id, Key: r.Entry.Key, RepoYAML: string(raw), Upstream: r.Upstream, Notes: notes}
		if levelIndex(level) >= levelIndex(LevelReleases) {
			pl.Releases, _ = m.Lib.Releases(ctx, id)
		}
		if levelIndex(level) >= levelIndex(LevelCapsules) {
			if dir, err := artifact.CapsuleDir(m.Lib.Layout.Root, r.R().Source); err == nil {
				for _, c := range artifact.ReadCapsules(dir) {
					files, err := artifact.ReadFiles(c.Path)
					if err == nil {
						pl.Capsules = append(pl.Capsules, CapsulePayload{Manifest: c, Files: files})
					}
				}
			}
		}
		out = append(out, pl)
	}
	return out, nil
}

func levelIndex(l string) int {
	for i, x := range Levels {
		if x == l {
			return i
		}
	}
	return -1
}

