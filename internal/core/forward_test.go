// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestForwardPush passes a push in RepoShelf on to the original, only when
// switched on and never by force (D-029, T-9.14).
func TestForwardPush(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("me", "tool")
	f.Git.Run(ctx, up.Bare, nil, "config", "http.receivepack", "true")
	l, _ := openLib(t, f)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("me", "tool"), Kind: repometa.KindOwn, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	id := r.R().ID

	// A push into RepoShelf (here: straight into the bare repository).
	work := filepath.Join(t.TempDir(), "w")
	push := func(file string) string {
		t.Helper()
		run := func(dir string, args ...string) string {
			out, err := f.Git.Run(ctx, dir, nil, args...)
			if err != nil {
				t.Fatalf("git %v: %v", args, err)
			}
			return strings.TrimSpace(string(out))
		}
		if _, err := f.Git.Run(ctx, work, nil, "status"); err != nil {
			run("", "clone", "--quiet", r.Paths.Mirror, work)
		} else {
			run(work, "pull", "--quiet", "--rebase=false", "origin", "main")
		}
		run(work, "-c", "user.name=t", "-c", "user.email=t@x", "commit", "--quiet", "--allow-empty", "-m", file)
		run(work, "push", "--quiet", "origin", "HEAD:main")
		return run(work, "rev-parse", "HEAD")
	}
	head := func() string {
		out, _ := f.Git.Run(ctx, up.Bare, nil, "rev-parse", "main")
		return strings.TrimSpace(string(out))
	}

	// Off by default: nothing leaves RepoShelf.
	c1 := push("eins")
	l.RefreshAfterPush(ctx, id, "git:me")
	if head() == c1 {
		t.Fatal("push forwarded although switched off")
	}

	s := Settings{Kind: repometa.KindOwn, Mode: repometa.SyncManual, Include: r.R().Include, PushUpstream: true}
	if _, err := l.UpdateSettings(ctx, id, s, "web"); err != nil {
		t.Fatal(err)
	}
	c2 := push("zwei")
	l.RefreshAfterPush(ctx, id, "git:me")
	r, _ = l.Get(ctx, id)
	if head() != c2 || r.Upstream.Forwarded == nil || r.Upstream.ForwardError != "" {
		t.Fatalf("not forwarded: head %s want %s, upstream %+v", head(), c2, r.Upstream.ForwardError)
	}

	// The original moved on: no force, the error names the branch.
	up.Git("pull", "--quiet", "origin", "main")
	foreign := up.Commit("fremd.txt", "x", "fremd")
	up.Push()
	push("drei")
	l.RefreshAfterPush(ctx, id, "git:me")
	r, _ = l.Get(ctx, id)
	if !strings.Contains(r.Upstream.ForwardError, "main") || head() != foreign {
		t.Errorf("rejected push: error %q, head %s", r.Upstream.ForwardError, head())
	}

	// Mirrors cannot forward.
	if _, err := l.Update(ctx, id, func(rr *repometa.Repo) error { rr.Kind = repometa.KindMirror; return nil }); err == nil {
		t.Error("mirror with push_upstream accepted")
	}
}
