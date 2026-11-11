// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/mcp"
	"github.com/Fenron-dev/reposhelf/internal/store"
	"github.com/Fenron-dev/reposhelf/internal/web"
)

// Panel is what the repository page shows about builds and capsules.
type Panel struct {
	Capsules []artifact.Capsule
	Builds   []artifact.Build
	Targets  []string
	Versions []string
}

// Panel implements web.Builds.
func (m *Manager) Panel(r *http.Request, repoID string) any {
	p := Panel{Targets: m.TargetNames(r.Context(), repoID), Builds: m.List(repoID, 5)}
	p.Capsules, _ = m.Capsules(r.Context(), repoID)
	if repo, err := m.Lib.Get(r.Context(), repoID); err == nil {
		if vs, err := m.Lib.Git.Versions(r.Context(), repo.Paths.Mirror); err == nil {
			for i, v := range vs {
				if i >= 20 {
					break
				}
				p.Versions = append(p.Versions, v.Name)
			}
		}
	}
	return p
}

type buildsPage struct {
	Runners  []RunnerInfo
	Builds   []artifact.Build
	Repos    []store.Entry
	Repo     string
	Targets  []string
	Now      time.Time
	CanBuild bool
	Names    map[string]string
}

type buildPage struct {
	B        artifact.Build
	Log      string
	Now      time.Time
	CanAdmin bool
	CanBuild bool
}

// Routes implements web.Builds.
func (m *Manager) Routes(s *web.Server, mux *http.ServeMux) {
	mux.Handle("GET /builds", s.Need(auth.Read, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := s.Identity(r)
		d := buildsPage{Runners: m.Runners(r.Context()), Builds: m.List(r.URL.Query().Get("repo"), 100), Repo: r.URL.Query().Get("repo"),
			Now: time.Now(), CanBuild: tok.Has(auth.Build), Names: map[string]string{}}
		d.Repos, _ = m.Lib.List(r.Context(), store.Query{Sort: store.SortName})
		for _, e := range d.Repos {
			d.Names[e.ID] = e.Owner + " / " + e.Name
		}
		if d.Repo != "" {
			d.Targets = m.TargetNames(r.Context(), d.Repo)
		}
		s.Render(w, r, "builds", "Builds & Runner", "builds", d)
	}))
	mux.Handle("GET /builds/{id}", s.Need(auth.Read, func(w http.ResponseWriter, r *http.Request) {
		b, err := m.Get(r.PathValue("id"))
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		log, _ := m.BuildLog(b.ID)
		tok, _ := s.Identity(r)
		s.Render(w, r, "build", "Build "+b.Target, "builds", buildPage{B: b, Log: log, Now: time.Now(), CanAdmin: tok.Has(auth.Admin), CanBuild: tok.Has(auth.Build)})
	}))
	mux.Handle("GET /builds/{id}/log", s.Need(auth.Read, func(w http.ResponseWriter, r *http.Request) {
		log, err := m.BuildLog(r.PathValue("id"))
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(log))
	}))
	mux.Handle("GET /builds/{id}/artifact", s.Need(auth.Read, func(w http.ResponseWriter, r *http.Request) {
		b, err := m.Get(r.PathValue("id"))
		if err != nil {
			s.Fail(w, r, err)
			return
		}
		name := r.URL.Query().Get("name")
		if _, err := m.ArtifactPath(b.ID, name); err != nil {
			s.Fail(w, r, err)
			return
		}
		web.ServeFileSafe(w, r, b.Dir+"/"+artifact.ArtifactsDir, name)
	}))
	mux.Handle("POST /builds", s.Need(auth.Build, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		tok, _ := s.Identity(r)
		b, err := m.StartBuild(r.Context(), StartRequest{Repo: r.Form.Get("repo"), Ref: r.Form.Get("ref"), Target: r.Form.Get("target"),
			Runner: r.Form.Get("runner"), Actor: "web:" + tok.Name, Confirm: tok.Has(auth.Admin), Version: r.Form.Get("version")})
		if err != nil {
			s.Fail(w, r, wrap(err))
			return
		}
		web.Redirect(w, r, "/builds/"+b.ID)
	}))
	mux.Handle("POST /builds/{id}/confirm", s.Need(auth.Admin, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := s.Identity(r)
		if err := m.Confirm(r.PathValue("id"), "web:"+tok.Name); err != nil {
			s.Fail(w, r, wrap(err))
			return
		}
		web.Redirect(w, r, "/builds/"+r.PathValue("id"))
	}))
	mux.Handle("POST /builds/{id}/cancel", s.Need(auth.Build, func(w http.ResponseWriter, r *http.Request) {
		tok, _ := s.Identity(r)
		if err := m.Cancel(r.PathValue("id"), "web:"+tok.Name); err != nil {
			s.Fail(w, r, wrap(err))
			return
		}
		web.Redirect(w, r, "/builds/"+r.PathValue("id"))
	}))
	// Load a runtime image into this instance's Docker (T-9.19).
	mux.Handle("POST /builds/{id}/load", s.Need(auth.Build, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		id := r.PathValue("id")
		out, err := m.LoadImage(r.Context(), id, r.Form.Get("name"))
		if err != nil {
			s.Fail(w, r, wrap(err))
			return
		}
		tok, _ := s.Identity(r)
		m.Lib.Record(core.Activity{Actor: "web:" + tok.Name, Action: "image-load", Detail: out})
		web.Redirect(w, r, web.WithMsg("/builds/"+id, "ok", "In Docker geladen: "+out))
	}))
}

func wrap(err error) error {
	switch {
	case errors.Is(err, core.ErrNotFound), errors.Is(err, core.ErrInvalid):
		return err
	case errors.Is(err, ErrTarget), errors.Is(err, ErrState), errors.Is(err, ErrDisabled):
		return errors.Join(core.ErrInvalid, err)
	}
	return err
}

// MCP adapts the manager to the MCP build tools (T-7.6).
type MCP struct{ M *Manager }

// Start implements mcp.Builds. Agent builds always need the user's
// confirmation when the runner asks for it.
func (a MCP) Start(ctx context.Context, repo, ref, target, actor string) (mcp.BuildInfo, error) {
	b, err := a.M.StartBuild(ctx, StartRequest{Repo: repo, Ref: ref, Target: target, Actor: actor})
	if err != nil {
		return mcp.BuildInfo{}, err
	}
	return info(b, ""), nil
}

// Get implements mcp.Builds.
func (a MCP) Get(_ context.Context, id string) (mcp.BuildInfo, error) {
	b, err := a.M.Get(id)
	if err != nil {
		return mcp.BuildInfo{}, err
	}
	log, _ := a.M.BuildLog(id)
	const maxLog = 20000
	if len(log) > maxLog {
		log = "…" + log[len(log)-maxLog:]
	}
	return info(b, log), nil
}

func info(b artifact.Build, log string) mcp.BuildInfo {
	bi := mcp.BuildInfo{ID: b.ID, Repo: b.Key, Ref: b.Ref, Target: b.Target, Runner: b.Runner, Status: b.Status, Log: log}
	if b.Status == artifact.StatusWaiting {
		bi.Status += " (wartet auf Bestätigung durch den Menschen)"
	}
	if b.Error != "" {
		bi.Log = strings.TrimSpace(bi.Log + "\n" + b.Error)
	}
	for _, a := range b.Artifacts {
		bi.Artifacts = append(bi.Artifacts, a.Name)
	}
	return bi
}
