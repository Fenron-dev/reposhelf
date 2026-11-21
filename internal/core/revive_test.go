// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

type memKC map[string]string

func (m memKC) Get(h string) (string, error) { return m[h], nil }
func (m memKC) Set(h, t string) error        { m[h] = t; return nil }
func (m memKC) Delete(h string) error        { delete(m, h); return nil }

// TestRevive: an archived repository goes back to a host – new repository,
// all branches and tags, releases with their files (V-5).
func TestRevive(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("weg", "tool")
	up.Git("tag", "v1.0")
	up.Git("branch", "feature")
	up.Push()
	up.Git("push", "--quiet", "origin", "--tags")
	up.AddRelease("v1.0", "2026-10-01T00:00:00Z", "tool.zip", "ZIPDATA")

	l, err := Open(ctx, Options{DataDir: t.TempDir(), Git: f.Git, Keychain: memKC{hostName(f.Host): "tok_1234567890", f.Host: "tok_1234567890"},
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceKinds:    map[string]string{f.Host: sources.KindGitHub},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()}})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	r, err := l.Add(ctx, AddRequest{URL: f.URL("weg", "tool"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if hosts := l.PublishHosts(ctx); len(hosts) == 0 {
		t.Fatalf("no publish host")
	}
	if err := l.Revive(ctx, r.R().ID, ReviveRequest{Host: f.Host, Name: "../x", Actor: "web"}); err == nil {
		t.Error("bad name accepted")
	}
	if err := l.Revive(ctx, r.R().ID, ReviveRequest{Host: f.Host, Name: "tool-neu", Releases: true, Actor: "web"}); err != nil {
		t.Fatal(err)
	}
	var st *ReviveState
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		r, _ = l.Get(ctx, r.R().ID)
		if st = r.Upstream.Revive; st != nil && !st.Running {
			break
		}
	}
	if st == nil || st.Running || st.Error != "" || st.Releases != 1 || st.Files != 1 || !strings.HasSuffix(st.URL, "/dennis/tool-neu") {
		t.Fatalf("revive = %+v", st)
	}
	refs, err := f.Git.Run(ctx, f.Root+"/dennis/tool-neu.git", nil, "for-each-ref", "--format=%(refname)")
	if err != nil || !strings.Contains(string(refs), "refs/heads/feature") || !strings.Contains(string(refs), "refs/tags/v1.0") {
		t.Errorf("pushed refs = %s, %v", refs, err)
	}
	if string(f.Uploaded["dennis/tool-neu/v1.0/tool.zip"]) != "ZIPDATA" {
		t.Errorf("uploaded = %v", f.Uploaded)
	}
}
