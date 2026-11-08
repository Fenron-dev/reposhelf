// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/runner"
)

// Builds lets paired instances start builds here (runner role) – only when
// the user allowed it for that peer; the build then follows the local
// confirmation rule.
type Builds interface {
	StartBuild(ctx context.Context, req runner.StartRequest) (artifact.Build, error)
	Get(id string) (artifact.Build, error)
}

// SetBuilds connects the local runner (nil = no builds for peers).
func (m *Manager) SetBuilds(b Builds) { m.builds = b }

type buildRequest struct {
	Repo, Ref, Target string
}

func (m *Manager) acceptBuild(w http.ResponseWriter, r *http.Request, p Peer, body []byte) {
	if !p.AllowBuilds || m.builds == nil {
		http.Error(w, "Builds für diese Instanz nicht freigegeben", http.StatusForbidden)
		return
	}
	var req buildRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	b, err := m.builds.StartBuild(r.Context(), runner.StartRequest{Repo: req.Repo, Ref: req.Ref, Target: req.Target, Actor: "peer:" + p.Name})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"id": b.ID, "status": b.Status})
}

func (m *Manager) buildStatus(w http.ResponseWriter, r *http.Request, p Peer, _ []byte) {
	if m.builds == nil {
		http.NotFound(w, r)
		return
	}
	b, err := m.builds.Get(r.PathValue("id"))
	if err != nil || b.Actor != "peer:"+p.Name {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]any{"id": b.ID, "status": b.Status, "error": b.Error, "artifacts": b.Artifacts})
}

// Remote is a paired instance seen as a runner (runner.Remote).
type Remote struct {
	M    *Manager
	Peer Peer
}

// Name implements runner.Remote.
func (r Remote) Name() string { return r.Peer.Name }

// Capabilities implements runner.Remote.
func (r Remote) Capabilities() []string { return r.Peer.Capabilities }

// Online implements runner.Remote.
func (r Remote) Online(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var info Info
	return r.M.getJSON(ctx, r.Peer, "/peer/info", &info) == nil && info.Runner
}

// Start implements runner.Remote: start there, then wait for the result.
func (r Remote) Start(ctx context.Context, b artifact.Build) (string, error) {
	var resp struct{ ID, Status string }
	if err := r.M.postJSON(ctx, r.Peer.URL+"/peer/builds", buildRequest{Repo: b.RepoID, Ref: b.Ref, Target: b.Target}, &resp, true, r.Peer); err != nil {
		return "", err
	}
	ext := r.Peer.URL + "/builds/" + resp.ID
	for {
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := r.M.getJSON(ctx, r.Peer, "/peer/builds/"+resp.ID, &st); err != nil {
			return ext, err
		}
		switch st.Status {
		case artifact.StatusSuccess:
			return ext, nil
		case artifact.StatusFailed, artifact.StatusCancelled:
			return ext, fmt.Errorf("build on %s: %s %s", r.Peer.Name, st.Status, st.Error)
		}
		select {
		case <-ctx.Done():
			return ext, ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
}

// RegisterRemotes adds all peers with runner capabilities to the runner.
func (m *Manager) RegisterRemotes(rm *runner.Manager) {
	for _, p := range m.Peers() {
		if len(p.Capabilities) > 0 {
			rm.AddRemote(Remote{M: m, Peer: p})
		}
	}
}
