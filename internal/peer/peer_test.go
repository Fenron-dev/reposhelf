// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/server"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

type instance struct {
	lib  *core.Library
	m    *Manager
	http *httptest.Server
}

func newInstance(t *testing.T, f *testutil.Forge, name string) *instance {
	t.Helper()
	lib, err := core.Open(context.Background(), core.Options{DataDir: t.TempDir(), Git: f.Git, NoKeychain: true,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceKinds:    map[string]string{f.Host: sources.KindGitHub},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	cfg := lib.Config()
	cfg.Instance.Name = name
	lib.SaveConfig(cfg)
	m, err := New(lib, logx.Discard())
	if err != nil {
		t.Fatal(err)
	}
	inst := &instance{lib: lib, m: m}
	srv, err := server.New(lib, server.Options{LookupEnv: func(string) (string, bool) { return "", false },
		ExtraAuth: m.CheckTemp, Extra: map[string]http.Handler{"/peer/": m.Handler()}})
	if err != nil {
		t.Fatal(err)
	}
	srv.Web.Ext.Peers = m
	inst.http = httptest.NewServer(srv.Handler())
	t.Cleanup(inst.http.Close)
	m.PublicURL = func() string { return inst.http.URL }
	return inst
}

func TestPairAndSync(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("fenron", "fero")
	up.AddRelease("v1.0", "2026-10-01T00:00:00Z", "fero.zip", "ZIP")
	a := newInstance(t, f, "Unraid")
	b := newInstance(t, f, "MacBook")

	repo, err := a.lib.Add(ctx, core.AddRequest{URL: f.URL("fenron", "fero"), Tags: []string{"medien"}, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	id := repo.R().ID

	// Wrong code is refused; the right one pairs both sides.
	if _, err := b.m.Pair(ctx, a.http.URL, "WRONG-CODE"); err == nil {
		t.Fatal("wrong code accepted")
	}
	code, _ := a.m.NewCode()
	p, err := b.m.Pair(ctx, a.http.URL, code)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != a.m.OwnFingerprint() || len(a.m.Peers()) != 1 || a.m.Peers()[0].ID != b.m.OwnFingerprint() {
		t.Fatalf("fingerprints do not match: %s vs %s", p.ID, a.m.OwnFingerprint())
	}
	if _, err := b.m.Pair(ctx, a.http.URL, code); err == nil {
		t.Error("pairing code reused")
	}

	// Releases level: metadata, code and release files.
	b.m.UpdatePeer(p.ID, func(x *Peer) { x.Selections = []Selection{{Type: "all", Level: LevelReleases}} })
	rep, err := b.m.Sync(ctx, p.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Created) != 1 || rep.Fetched != 1 || rep.Assets != 1 || len(rep.Problems) != 0 {
		t.Fatalf("first sync = %+v", rep)
	}
	got, err := b.lib.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !b.lib.Git.HasCommits(ctx, got.Paths.Mirror) || got.R().Tags[0] != "medien" {
		t.Errorf("adopted repo incomplete: %+v", got.R())
	}
	if got.R().Source.URL != repo.R().Source.URL {
		t.Error("origin URL replaced by the peer")
	}
	if p, err := b.lib.AssetPath(ctx, id, "v1.0", "fero.zip"); err != nil {
		t.Errorf("asset not synced: %v", err)
	} else if bb, _ := os.ReadFile(p); string(bb) != "ZIP" {
		t.Errorf("asset = %q", bb)
	}

	// Change only on A → taken over.
	a.lib.SetTags(ctx, id, []string{"medien", "rust"}, "t")
	if _, err := b.m.Sync(ctx, p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	got, _ = b.lib.Get(ctx, id)
	if !slices.Equal(got.R().Tags, []string{"medien", "rust"}) {
		t.Errorf("tags = %v", got.R().Tags)
	}
	// Change only on B → kept.
	b.lib.Pin(ctx, id, "v1.0", true, "t")
	b.m.Sync(ctx, p.ID, "test")
	got, _ = b.lib.Get(ctx, id)
	if !slices.Contains(got.R().Retention.Pinned, "v1.0") {
		t.Error("local change overwritten")
	}
	// Both change the same field → conflict, nothing overwritten.
	a.lib.SetTags(ctx, id, []string{"von-a"}, "t")
	b.lib.SetTags(ctx, id, []string{"von-b"}, "t")
	rep, _ = b.m.Sync(ctx, p.ID, "test")
	got, _ = b.lib.Get(ctx, id)
	cs := b.m.Conflicts()
	if rep.Conflicts != 1 || len(cs) != 1 || got.R().Tags[0] != "von-b" {
		t.Fatalf("conflict handling: rep %+v conflicts %+v tags %v", rep, cs, got.R().Tags)
	}
	if err := b.m.Resolve(ctx, cs[0].ID, "remote", "t"); err != nil {
		t.Fatal(err)
	}
	got, _ = b.lib.Get(ctx, id)
	if got.R().Tags[0] != "von-a" || len(b.m.Conflicts()) != 0 {
		t.Errorf("resolve: tags %v, conflicts %d", got.R().Tags, len(b.m.Conflicts()))
	}

	// Unsigned or unknown callers get nothing.
	resp, _ := http.Post(a.http.URL+"/peer/repos", "application/json", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned request = %d", resp.StatusCode)
	}
	if _, ok := a.m.CheckTemp("rsp_unknown"); ok {
		t.Error("unknown temp token accepted")
	}
	// Unpair.
	if err := b.m.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.m.Sync(ctx, p.ID, "t"); !errors.Is(err, ErrUnknownPeer) {
		t.Errorf("sync after unpair = %v", err)
	}
	_ = repometa.KindMirror
}

func TestPeerPage(t *testing.T) {
	f := testutil.NewForge(t)
	a := newInstance(t, f, "A")
	resp, err := http.Get(a.http.URL + "/peers")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET /peers = %v %v", resp, err)
	}
}

func TestTempTokens(t *testing.T) {
	f := testutil.NewForge(t)
	a := newInstance(t, f, "A")
	tok := a.m.issueTemp("FP-1")
	got, ok := a.m.CheckTemp(tok)
	if !ok || !got.Has("read") || got.Has("write") {
		t.Errorf("temp token = %+v %v", got, ok)
	}
}
