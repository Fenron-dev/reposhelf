// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/web"
)

type page struct {
	Own         string
	Name        string
	URL         string
	Peers       []Peer
	Conflicts   []Conflict
	Code        string
	CodeExpires time.Time
	Collections []core.Collection
	Levels      []string
}

// Routes implements web.Peers (admin only: pairing grants access).
func (m *Manager) Routes(s *web.Server, mux *http.ServeMux) {
	render := func(w http.ResponseWriter, r *http.Request, code string, exp time.Time) {
		p := page{Own: m.OwnFingerprint(), Name: m.Lib.Config().Instance.Name, URL: m.PublicURL(), Peers: m.Peers(),
			Conflicts: m.Conflicts(), Code: code, CodeExpires: exp, Levels: Levels}
		p.Collections, _ = m.Lib.Collections(r.Context())
		s.Render(w, r, "peers", "Abgleich zwischen Instanzen", "peers", p)
	}
	mux.Handle("GET /peers", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) { render(w, r, "", time.Time{}) }))
	mux.Handle("POST /peers/code", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		code, exp := m.NewCode()
		w.Header().Set("Cache-Control", "no-store")
		render(w, r, code, exp)
	}))
	mux.Handle("POST /peers/pair", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p, err := m.Pair(r.Context(), r.Form.Get("url"), r.Form.Get("code"))
		if err != nil {
			s.Fail(w, r, errorsJoin(core.ErrInvalid, err))
			return
		}
		web.Redirect(w, r, web.WithMsg("/peers", "ok", "Gekoppelt mit „"+p.Name+"“ – Fingerprint "+p.ID+". Vergleiche ihn mit der Anzeige dort."))
	}))
	mux.Handle("POST /peers/{id}/sync", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		name, _ := s.IdentityOf(r)
		rep, err := m.Sync(r.Context(), r.PathValue("id"), "web:"+name)
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		web.Redirect(w, r, web.WithMsg("/peers", "ok", rep.Summary()))
	}))
	mux.Handle("POST /peers/{id}/settings", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		var sels []Selection
		types, refs, levels := r.Form["type"], r.Form["ref"], r.Form["level"]
		for i := range types {
			if types[i] == "" || i >= len(levels) {
				continue
			}
			ref := ""
			if i < len(refs) {
				ref = strings.TrimSpace(refs[i])
			}
			if types[i] != "all" && ref == "" {
				continue
			}
			sels = append(sels, Selection{Type: types[i], Ref: ref, Level: levels[i]})
		}
		err := m.UpdatePeer(r.PathValue("id"), func(p *Peer) {
			p.Selections = sels
			p.Interval = strings.TrimSpace(r.Form.Get("interval"))
			p.AllowBuilds = r.Form.Get("allow_builds") == "on"
		})
		if err != nil {
			s.Fail(w, r, errorsJoin(core.ErrNotFound, err))
			return
		}
		web.Redirect(w, r, web.WithMsg("/peers", "ok", "Gespeichert."))
	}))
	mux.Handle("POST /peers/{id}/remove", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		if err := m.Remove(r.PathValue("id")); err != nil {
			s.Fail(w, r, errorsJoin(core.ErrNotFound, err))
			return
		}
		web.Redirect(w, r, web.WithMsg("/peers", "ok", "Kopplung aufgehoben. Übernommene Daten bleiben erhalten."))
	}))
	mux.Handle("POST /peers/resolve", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		name, _ := s.IdentityOf(r)
		if err := m.Resolve(r.Context(), r.Form.Get("id"), r.Form.Get("choice"), "web:"+name); err != nil {
			s.Fail(w, r, err)
			return
		}
		web.Redirect(w, r, "/peers")
	}))
}

func errorsJoin(kind error, err error) error {
	return &joined{kind: kind, err: err}
}

type joined struct{ kind, err error }

func (j *joined) Error() string   { return j.err.Error() }
func (j *joined) Unwrap() []error { return []error{j.kind, j.err} }

// Schedule runs automatic syncs for peers with an interval.
func (m *Manager) Schedule(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, p := range m.Peers() {
				if p.Interval == "" {
					continue
				}
				d, err := repometa.ParseDuration(p.Interval)
				if err != nil || (p.LastSync != nil && time.Since(*p.LastSync) < time.Duration(d)) {
					continue
				}
				if _, err := m.Sync(ctx, p.ID, "scheduler"); err != nil {
					m.Log.Warn("peer sync", "peer", p.Name, "err", err)
				}
			}
		}
	}
}
