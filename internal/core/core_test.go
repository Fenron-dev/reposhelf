// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// clock is a controllable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func openLib(t *testing.T, f *testutil.Forge) (*Library, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)}
	opts := Options{
		DataDir:    t.TempDir(),
		LookupEnv:  func(string) (string, bool) { return "", false },
		Now:        clk.Now,
		Git:        f.Git,
		NoKeychain: true,
		SourceKinds: map[string]string{
			f.Host: sources.KindGitHub,
		},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()},
	}
	l, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, clk
}

func TestAddSyncAndProtectHistory(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("fenron", "fero")
	up.Git("tag", "v1.0")
	up.Push()
	up.AddRelease("v1.0", "2026-10-01T00:00:00Z", "fero.zip", "ZIPDATA")
	l, clk := openLib(t, f)

	r, err := l.Add(ctx, AddRequest{URL: f.URL("fenron", "fero"), Tags: []string{"medien/tool"}, Wait: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if r.Status() != StatusOK || r.Upstream.Metadata == nil || r.Upstream.Metadata.Description != "Repo fero" {
		t.Fatalf("after add: status %s, upstream %+v", r.Status(), r.Upstream)
	}
	if r.R().License.SPDX == nil || *r.R().License.SPDX != "MIT" {
		t.Errorf("license = %v", r.R().License.SPDX)
	}
	if r.Upstream.Stats.Branches != 1 || r.Upstream.Stats.Versions != 1 || r.Upstream.Stats.Releases != 1 {
		t.Errorf("stats = %+v", r.Upstream.Stats)
	}
	p, err := l.AssetPath(ctx, r.R().ID, "v1.0", "fero.zip")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "ZIPDATA" {
		t.Errorf("asset = %q", b)
	}
	// Duplicate refused (also with different case – same folder on macOS).
	if _, err := l.Add(ctx, AddRequest{URL: strings.Replace(f.URL("fenron", "fero"), "fero", "FERO", 1)}); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate add = %v", err)
	}

	// Force-push upstream: the old state is backed up.
	first := up.Git("rev-parse", "HEAD")
	up.Commit("a.txt", "a", "second")
	up.Push()
	if _, err := l.Sync(ctx, r.R().ID, "test"); err != nil {
		t.Fatal(err)
	}
	up.Git("reset", "--hard", "--quiet", first)
	up.Commit("b.txt", "b", "rewrite")
	up.ForcePush()
	clk.Add(time.Hour)
	rep, err := l.Sync(ctx, r.R().ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Mirror.Rewritten) != 1 || rep.Mirror.Backup == "" {
		t.Fatalf("rewrite not backed up: %+v", rep.Mirror)
	}
	backups, _ := l.Git.Backups(ctx, r.Paths.Mirror)
	if len(backups) != 1 {
		t.Errorf("backups = %+v", backups)
	}

	// The original disappears: status gone, data stays.
	up.SetGone(true)
	clk.Add(time.Hour)
	if _, err := l.Sync(ctx, r.R().ID, "test"); err == nil {
		t.Fatal("sync of vanished repo succeeded")
	}
	r, _ = l.Get(ctx, r.R().ID)
	if r.Status() != StatusGone || r.R().Sync.LastError == nil {
		t.Errorf("status = %s, err %v", r.Status(), r.R().Sync.LastError)
	}
	if !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		t.Error("mirror lost its content")
	}
	// User override wins.
	ov := repometa.UpstreamArchived
	s := Settings{Mode: repometa.SyncFollow, Include: r.R().Include, StatusOverride: &ov}
	if r, err = l.UpdateSettings(ctx, r.R().ID, s, "test"); err != nil || r.Status() != StatusArchived {
		t.Errorf("override: %v %s", err, r.Status())
	}

	// Reindex from files gives the same picture.
	res, err := l.Reindex(ctx)
	if err != nil || res.Repos != 1 || len(res.Problems) != 0 {
		t.Fatalf("reindex = %+v, %v", res, err)
	}
	e, _ := l.Store.Get(ctx, r.R().ID)
	if e.Status != StatusArchived || e.Tags[0] != "medien/tool" {
		t.Errorf("reindexed entry = %+v", e)
	}
	if hits, _ := l.List(ctx, store.Query{Text: "fero"}); len(hits) != 1 {
		t.Errorf("full-text search after reindex = %d", len(hits))
	}
}

func TestHostHealthAvoidsFalseGone(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	a := f.NewRepo("o", "a")
	b := f.NewRepo("o", "b")
	l, clk := openLib(t, f)
	ra, _ := l.Add(ctx, AddRequest{URL: f.URL("o", "a"), Wait: true})
	rb, _ := l.Add(ctx, AddRequest{URL: f.URL("o", "b"), Wait: true})

	// The whole host answers 404 for more than 24 h (outage).
	a.SetGone(true)
	b.SetGone(true)
	l.Sync(ctx, ra.R().ID, "t")
	l.Sync(ctx, rb.R().ID, "t")
	clk.Add(25 * time.Hour)
	l.Sync(ctx, ra.R().ID, "t")
	l.Sync(ctx, rb.R().ID, "t")
	got, _ := l.Get(ctx, rb.R().ID)
	if got.Status() != StatusDegraded || !got.Upstream.HostDown {
		t.Errorf("host outage judged as %s (host down %v)", got.Status(), got.Upstream.HostDown)
	}
	// One repository comes back: the other is really gone.
	a.SetGone(false)
	l.Sync(ctx, ra.R().ID, "t")
	clk.Add(time.Hour)
	l.Sync(ctx, rb.R().ID, "t")
	got, _ = l.Get(ctx, rb.R().ID)
	if got.Status() != StatusGone {
		t.Errorf("status = %s, want gone", got.Status())
	}
}

func TestOrganisation(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	f.NewRepo("o", "tool")
	plug := f.NewRepo("o", "plugin")
	plug.Commit("go.mod", "module x\n\nrequire "+f.Host+"/o/tool v1.0.0\n", "deps")
	plug.Push()
	l, _ := openLib(t, f)
	tool, _ := l.Add(ctx, AddRequest{URL: f.URL("o", "tool"), Tags: []string{"ttrpg/tool"}, Wait: true})
	pl, err := l.Add(ctx, AddRequest{URL: f.URL("o", "plugin"), Tags: []string{"ttrpg/plugin"}, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	// Dependency suggestion.
	found := false
	for _, rel := range pl.R().Relations {
		if rel.Type == "depends_on" && rel.State == repometa.RelationSuggested && strings.HasSuffix(rel.Target, "/o/tool") {
			found = true
		}
	}
	if !found {
		t.Errorf("no dependency suggestion: %+v", pl.R().Relations)
	}
	// Confirm → backlink on the tool.
	if _, err := l.SetRelation(ctx, pl.R().ID, "depends_on", tool.Entry.Key, repometa.RelationConfirmed, "t"); err != nil {
		t.Fatal(err)
	}
	tool, _ = l.Get(ctx, tool.R().ID)
	if len(tool.Backlinks) != 1 || tool.Backlinks[0].From != pl.R().ID {
		t.Errorf("backlinks = %+v", tool.Backlinks)
	}
	// Rejected suggestions do not come back.
	l.SetRelation(ctx, pl.R().ID, "depends_on", tool.Entry.Key, repometa.RelationRejected, "t")
	l.Sync(ctx, pl.R().ID, "t")
	pl, _ = l.Get(ctx, pl.R().ID)
	for _, rel := range pl.R().Relations {
		if rel.Type == "depends_on" && rel.State != repometa.RelationRejected {
			t.Errorf("rejected relation came back: %+v", rel)
		}
	}

	// Tag hierarchy and global rename.
	tree, _ := l.TagTree(ctx)
	if len(tree) != 1 || tree[0].Total != 2 || len(tree[0].Children) != 2 {
		t.Errorf("tag tree = %+v", tree)
	}
	if n, err := l.RenameTag(ctx, "ttrpg", "rpg", "t"); err != nil || n != 2 {
		t.Fatalf("rename = %d, %v", n, err)
	}
	if hits, _ := l.List(ctx, store.Query{Tag: "rpg/*"}); len(hits) != 2 {
		t.Errorf("after rename: %d", len(hits))
	}

	// Collections, smart collections, retention inheritance.
	keep := 1
	if err := l.SaveCollection(Collection{ID: "fundus", Name: "Fundus", Color: "#4a9", Retention: CollectionRetention{ReleasesKeep: &keep}}, "t"); err != nil {
		t.Fatal(err)
	}
	l.SetCollections(ctx, tool.R().ID, []string{"fundus"}, "t")
	tool, _ = l.Get(ctx, tool.R().ID)
	if ret := l.EffectiveRetention(tool); ret.ReleasesKeep != 1 || !strings.Contains(ret.From["releases_keep"], "Fundus") {
		t.Errorf("retention = %+v", ret)
	}
	smart := SmartFilter{Logic: "and", Rules: []SmartRule{{Field: "tag", Op: "contains", Value: "rpg/*"}, {Field: "name", Op: "starts_with", Value: "plu"}}}
	if err := l.SaveCollection(Collection{ID: "plugins", Name: "Plugins", Smart: &smart}, "t"); err != nil {
		t.Fatal(err)
	}
	members, err := l.CollectionMembers(ctx, "plugins")
	if err != nil || len(members) != 1 || members[0].Name != "plugin" {
		t.Errorf("smart members = %+v, %v", members, err)
	}
	cols, _ := l.Collections(ctx)
	if len(cols) != 2 {
		t.Errorf("collections = %+v", cols)
	}

	// Notes with wiki links create confirmed relations.
	if err := l.WriteNotes(ctx, tool.R().ID, "Siehe [[plugin]] für Erweiterungen.", "t"); err != nil {
		t.Fatal(err)
	}
	tool, _ = l.Get(ctx, tool.R().ID)
	ok := false
	for _, rel := range tool.R().Relations {
		if rel.Source == "wikilink" && rel.State == repometa.RelationConfirmed {
			ok = true
		}
	}
	if !ok {
		t.Errorf("wiki link relation missing: %+v", tool.R().Relations)
	}
	if g, _ := l.Graph(ctx); len(g.Nodes) != 2 {
		t.Errorf("graph = %+v", g)
	}
}

func TestRetentionTrashRestorePurge(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("o", "rel")
	up.AddRelease("v1", "2026-01-01T00:00:00Z", "a.bin", "ONE")
	up.AddRelease("v2", "2026-02-01T00:00:00Z", "a.bin", "TWO")
	up.AddRelease("v3", "2026-03-01T00:00:00Z", "a.bin", "ONE") // same content as v1
	l, clk := openLib(t, f)
	cfg := l.Config()
	cfg.Retention.ReleasesKeep = 3
	if err := l.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "rel"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	rels, _ := l.Releases(ctx, r.R().ID)
	if len(rels) != 3 {
		t.Fatalf("releases = %d", len(rels))
	}
	// Deduplicated: v1 and v3 share content.
	p1, _ := l.AssetPath(ctx, r.R().ID, "v1", "a.bin")
	p3, _ := l.AssetPath(ctx, r.R().ID, "v3", "a.bin")
	i1, _ := os.Stat(p1)
	i3, _ := os.Stat(p3)
	if !os.SameFile(i1, i3) && !strings.Contains(p1+p3, "v1") {
		t.Errorf("no deduplication: %s %s", p1, p3)
	}

	// Lower the rule: v1 becomes a cleanup candidate; pinning v2 protects it.
	keep := 1
	l.UpdateSettings(ctx, r.R().ID, Settings{Mode: repometa.SyncFollow, Include: r.R().Include, Retention: repometa.Retention{ReleasesKeep: &keep}}, "t")
	l.Pin(ctx, r.R().ID, "v2", true, "t")
	plan, err := l.CleanupPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Kind != ItemReleaseAssets || !strings.Contains(plan[0].Label, "v1") {
		t.Fatalf("plan = %+v", plan)
	}
	if n, err := l.MoveToTrash(ctx, []string{plan[0].ID}, "t"); err != nil || n != 1 {
		t.Fatalf("trash = %d, %v", n, err)
	}
	if _, err := l.AssetPath(ctx, r.R().ID, "v1", "a.bin"); err == nil {
		t.Error("asset still there after trashing")
	}
	trash, _ := l.Trash()
	if len(trash) != 1 {
		t.Fatalf("trash = %+v", trash)
	}
	if err := l.Restore(ctx, trash[0].ID, "t"); err != nil {
		t.Fatal(err)
	}
	if p, err := l.AssetPath(ctx, r.R().ID, "v1", "a.bin"); err != nil {
		t.Errorf("not restored: %v", err)
	} else if b, _ := os.ReadFile(p); string(b) != "ONE" {
		t.Errorf("restored content = %q", b)
	}

	// Whole repository: mark, trash, restore.
	if err := l.MarkForDeletion(ctx, r.R().ID, true, "t"); err != nil {
		t.Fatal(err)
	}
	plan, _ = l.CleanupPlan(ctx)
	var repoItem string
	for _, it := range plan {
		if it.Kind == ItemRepo {
			repoItem = it.ID
		}
	}
	if _, err := l.MoveToTrash(ctx, []string{repoItem}, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(ctx, r.R().ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("repo still listed: %v", err)
	}
	trash, _ = l.Trash()
	if err := l.Restore(ctx, trash[0].ID, "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(ctx, r.R().ID); err != nil {
		t.Errorf("repo not restored: %v", err)
	}

	// Due entries need explicit purge.
	l.MarkForDeletion(ctx, r.R().ID, true, "t")
	plan, _ = l.CleanupPlan(ctx)
	for _, it := range plan {
		if it.Kind == ItemRepo {
			l.MoveToTrash(ctx, []string{it.ID}, "t")
		}
	}
	clk.Add(31 * 24 * time.Hour)
	rv, _ := l.Review(ctx)
	if len(rv.Due) != 1 {
		t.Fatalf("due = %+v", rv.Due)
	}
	if n, err := l.Purge(ctx, []string{rv.Due[0].ID}, "t"); err != nil || n != 1 {
		t.Errorf("purge = %d, %v", n, err)
	}
	if left, _ := os.ReadDir(l.Layout.TrashDir()); len(left) != 0 {
		t.Errorf("trash not empty: %v", left)
	}
}

func TestLocalForkAndScheduler(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	f.NewRepo("o", "orig")
	l, clk := openLib(t, f)
	orig, err := l.Add(ctx, AddRequest{URL: f.URL("o", "orig"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := l.Fork(ctx, orig.R().ID, "", "weiter", "t")
	if err != nil {
		t.Fatal(err)
	}
	if fork.R().Kind != repometa.KindFork || *fork.R().ForkOf != orig.R().ID || !l.Git.HasCommits(ctx, fork.Paths.Mirror) {
		t.Errorf("fork = %+v", fork.R())
	}
	local, err := l.CreateLocal(ctx, "", "neu", "Mein Projekt", []string{"idee"}, "t")
	if err != nil {
		t.Fatal(err)
	}
	if local.Status() != StatusLocal || local.Entry.Description != "Mein Projekt" {
		t.Errorf("local = %s %q", local.Status(), local.Entry.Description)
	}

	// Scheduler: follow repos become due after interval (+ spread).
	e, _ := l.Store.Get(ctx, orig.R().ID)
	next, ok := l.NextCheck(e)
	if !ok || !next.After(clk.Now()) {
		t.Errorf("next check = %v %v", next, ok)
	}
	if _, ok := l.NextCheck(local.Entry); ok {
		t.Error("local repo scheduled")
	}
	clk.Add(7 * time.Hour)
	l.enqueueDue(ctx)
	if !l.IsBusy(orig.R().ID) {
		t.Error("due repo not queued")
	}
	l.StartBackground()
	deadline := time.Now().Add(20 * time.Second)
	for l.IsBusy(orig.R().ID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if l.IsBusy(orig.R().ID) {
		t.Error("background sync did not finish")
	}
	if acts := l.RecentActivity(50, orig.R().ID); len(acts) < 2 {
		t.Errorf("activity = %+v", acts)
	}
	if fileExists(filepath.Join(l.Layout.Root, ".reposhelf", "logs", activityFile)) == false {
		t.Error("activity log missing")
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestImportZIP(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	l, _ := openLib(t, f)
	mk := func(files map[string]string) *zip.Reader {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for n, c := range files {
			w, _ := zw.Create(n)
			w.Write([]byte(c))
		}
		zw.Close()
		zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		return zr
	}
	r, err := l.ImportZIP(ctx, mk(map[string]string{"tool-main/README.md": "# Tool\n", "tool-main/src/a.go": "package a\n"}), "", "", "aus ZIP", nil, "t")
	if err != nil {
		t.Fatal(err)
	}
	if r.Entry.Name != "tool-main" || !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		t.Fatalf("import = %+v", r.Entry)
	}
	if b, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", "src/a.go", 0); err != nil || string(b) != "package a\n" {
		t.Errorf("content = %q %v", b, err)
	}
	if _, err := l.ImportZIP(ctx, mk(map[string]string{"../evil.txt": "x"}), "", "evil", "", nil, "t"); !errors.Is(err, ErrInvalid) {
		t.Errorf("zip slip = %v", err)
	}
}
