// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// TestPageGitCost guards page speed (T-9.9): every git process costs time,
// on Windows a lot. Common pages must not start git once warmed up, and
// the cost must not grow with the number of repositories.
func TestPageGitCost(t *testing.T) {
	e := setup(t)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		e.f.NewRepo("x", name)
		if _, err := e.lib.Add(context.Background(), core.AddRequest{URL: e.f.URL("x", name), Wait: true}); err != nil {
			t.Fatal(err)
		}
	}
	id := e.repo.R().ID
	visit := func(p string) int64 {
		before := gitcli.Calls()
		start := time.Now()
		resp, err := http.Get(e.http.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		n := gitcli.Calls() - before
		t.Logf("%-45s git=%2d %v", p, n, time.Since(start).Round(time.Millisecond))
		return n
	}
	// Warm up: first visits fill caches; the cleanup review runs in the
	// background.
	for _, p := range []string{"/", "/repo/" + id} {
		visit(p)
	}
	time.Sleep(500 * time.Millisecond)
	limits := map[string]int64{
		"/": 0, "/repo/" + id: 0, "/tags": 0, "/settings": 0, "/activity": 0, "/notes": 0,
		"/repo/" + id + "?tab=notes": 0, "/repo/" + id + "?tab=yaml": 0,
		"/repo/" + id + "?tab=versions": 3, "/repo/" + id + "/tree/": 3,
	}
	for p, max := range limits {
		if n := visit(p); n > max {
			t.Errorf("%s started %d git processes, at most %d allowed", p, n, max)
		}
	}
}

// TestSyncProgress checks the progress display of a queued sync and the
// reload once it is over (T-9.9).
func TestSyncProgress(t *testing.T) {
	e := setup(t)
	id := e.repo.R().ID
	resp, err := http.Get(e.http.URL + "/repo/" + id + "/progress")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("HX-Refresh") != "true" {
		t.Errorf("idle progress = %d, HX-Refresh %q", resp.StatusCode, resp.Header.Get("HX-Refresh"))
	}

	e.lib.Enqueue(id, "fenron/fero", "test") // background is not started: stays queued
	for path, want := range map[string]string{
		"/repo/" + id:               `hx-get="/repo/` + id + `/progress"`,
		"/repo/" + id + "/progress": "wartet",
		"/jobs":                     "<progress",
	} {
		if body := e.get(path, 200); !strings.Contains(body, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}
