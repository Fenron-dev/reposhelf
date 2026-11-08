// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// fakeActions emulates the GitHub Actions API: dispatch, run listing, run
// status, artifact listing and the artifact ZIP download.
func fakeActions(t *testing.T, zipData []byte) (*httptest.Server, *int32) {
	var dispatched int32
	var srv *httptest.Server
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /repos/o/app/actions/workflows/build.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["ref"] != "main" {
			t.Errorf("dispatch ref = %q", body["ref"])
		}
		atomic.StoreInt32(&dispatched, 1)
		w.WriteHeader(http.StatusNoContent)
	})
	polls := int32(0)
	mux.HandleFunc("GET /repos/o/app/actions/workflows/build.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		runs := []map[string]any{}
		if atomic.LoadInt32(&dispatched) == 1 {
			runs = append(runs, map[string]any{"id": 7, "status": "queued", "html_url": srv.URL + "/run/7", "created_at": time.Now().UTC().Format(time.RFC3339)})
		}
		json.NewEncoder(w).Encode(map[string]any{"workflow_runs": runs})
	})
	mux.HandleFunc("GET /repos/o/app/actions/runs/7", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		st := "in_progress"
		if atomic.AddInt32(&polls, 1) > 2 {
			st = "completed"
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "status": st, "conclusion": "success", "html_url": srv.URL + "/run/7"})
	})
	mux.HandleFunc("GET /repos/o/app/actions/runs/7/artifacts", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"artifacts": []map[string]any{
			{"name": "app linux/amd64", "archive_download_url": srv.URL + "/download/1", "size_in_bytes": len(zipData)}}})
	})
	mux.HandleFunc("GET /download/1", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		w.Write(zipData)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &dispatched
}

func TestGitHubActionsRunnerDownloadsArtifacts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	githubPoll = 20 * time.Millisecond

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	f, _ := zw.Create("app")
	f.Write([]byte("BINARY"))
	zw.Close()
	api, dispatched := fakeActions(t, zbuf.Bytes())

	forge := testutil.NewForge(t)
	up := forge.NewRepo("o", "app")
	up.Commit(".github/workflows/build.yml", "on:\n  workflow_dispatch:\njobs:\n  b:\n    runs-on: macos-latest\n    steps:\n      - run: echo\n", "wf")
	up.Push()

	lib, err := core.Open(ctx, core.Options{DataDir: t.TempDir(), Git: forge.Git, NoKeychain: true,
		LookupEnv: func(k string) (string, bool) {
			if k == "GITHUB_TOKEN" {
				return "tok", true
			}
			return "", false
		},
		SourceBaseURLs: map[string]string{"github.com": api.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	// A repository on github.com whose code comes from the local forge.
	r := repometa.Defaults(repometa.KindOwn)
	r.ID = repometa.NewID(time.Now())
	r.Source = repometa.Source{URL: "https://github.com/o/app.git", Host: "github.com", Owner: "o", Name: "app"}
	r.Sync = repometa.Sync{Mode: repometa.SyncManual}
	r.Created = time.Now().UTC().Truncate(time.Second)
	if _, err := lib.AdoptRepo(ctx, r, core.Upstream{Provider: "github"}, "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.FetchFromPeer(ctx, r.ID, forge.URL("o", "app"), nil, "test"); err != nil {
		t.Fatal(err)
	}

	m := New(lib, logx.Discard())
	m.Start(ctx)
	names := m.TargetNames(ctx, r.ID)
	if !strings.Contains(strings.Join(names, ","), "github:build.yml") {
		t.Fatalf("targets = %v", names)
	}
	b, err := m.StartBuild(ctx, StartRequest{Repo: r.ID, Target: "github:build.yml", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Runner != RunnerGitHub || b.Status != artifact.StatusQueued {
		t.Fatalf("build = %+v (GitHub builds need no local confirmation)", b)
	}
	b = wait(t, m, b.ID)
	log, _ := m.BuildLog(b.ID)
	if b.Status != artifact.StatusSuccess {
		t.Fatalf("status %s: %s\n%s", b.Status, b.Error, log)
	}
	if atomic.LoadInt32(dispatched) != 1 || !strings.HasSuffix(b.External, "/run/7") {
		t.Errorf("dispatch/run not tracked: %+v", b)
	}
	if len(b.Artifacts) != 1 || b.Artifacts[0].Name != "app_linux_amd64.zip" || b.Artifacts[0].SHA256 == "" {
		t.Fatalf("artifacts = %+v", b.Artifacts)
	}
	p, err := m.ArtifactPath(b.ID, b.Artifacts[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "app" {
		t.Errorf("stored artifact is not the ZIP: %v", err)
	}
}
