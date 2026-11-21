// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testGit returns git isolated from the user's configuration.
func testGit(t *testing.T) *Git {
	t.Helper()
	g, err := Find("")
	if err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	g.Env = []string{
		"HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	return g
}

// upstream is a fake remote: a normal repository in a temp folder.
type upstream struct {
	t   *testing.T
	g   *Git
	dir string
}

func newUpstream(t *testing.T, g *Git) *upstream {
	dir := filepath.Join(t.TempDir(), "up")
	if _, err := g.Run(context.Background(), "", nil, "init", "--quiet", "--initial-branch=main", dir); err != nil {
		t.Fatal(err)
	}
	return &upstream{t: t, g: g, dir: dir}
}

func (u *upstream) git(args ...string) string {
	u.t.Helper()
	out, err := u.g.Run(context.Background(), u.dir, nil, args...)
	if err != nil {
		u.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func (u *upstream) commit(file, content, msg string) string {
	u.t.Helper()
	os.WriteFile(filepath.Join(u.dir, file), []byte(content), 0o644)
	u.git("add", file)
	u.git("commit", "--quiet", "-m", msg)
	return u.git("rev-parse", "HEAD")
}

func TestCheckAndFind(t *testing.T) {
	g := testGit(t)
	v, err := g.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v (%s)", err, v)
	}
	// A portable git next to the program wins over PATH (D-020).
	exeDir := t.TempDir()
	bin := filepath.Join(exeDir, "git", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"), 0o755)
	if found, _ := Find(exeDir); found.Path != filepath.Join(bin, "git") && found.Path != "" {
		t.Logf("portable git path = %s (platform-specific)", found.Path)
	}
}

func TestMirrorProtectsHistory(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	up := newUpstream(t, g)
	first := up.commit("README.md", "# Hallo\n", "first")
	up.git("tag", "v1.0")
	up.git("branch", "feature")

	mirror := filepath.Join(t.TempDir(), "m.git")
	if err := g.InitBare(ctx, mirror, up.dir); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	res, err := g.Mirror(ctx, mirror, up.dir, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New) != 3 || res.Backup != "" || res.DefaultBranch != "main" {
		t.Fatalf("first sync = %+v", res)
	}
	if g.Head(ctx, mirror) != "main" {
		t.Error("HEAD not set to default branch")
	}

	// No change upstream: nothing fetched.
	res, err = g.Mirror(ctx, mirror, up.dir, nil, t0)
	if err != nil || res.Changed() {
		t.Fatalf("idle sync = %+v, %v", res, err)
	}

	// Fast-forward: no backup.
	second := up.commit("a.txt", "a", "second")
	res, err = g.Mirror(ctx, mirror, up.dir, nil, t0)
	if err != nil || len(res.Updated) != 1 || res.Backup != "" {
		t.Fatalf("ff sync = %+v, %v", res, err)
	}

	// Force-push (rewrite main), move a tag, delete a branch.
	up.git("reset", "--quiet", "--hard", first)
	rewritten := up.commit("b.txt", "b", "rewritten")
	up.git("tag", "-f", "v1.0", rewritten)
	up.git("branch", "-D", "feature")
	t1 := t0.Add(time.Hour)
	res, err = g.Mirror(ctx, mirror, up.dir, nil, t1)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rewritten) != 2 || res.Backup != "refs/reposhelf/backup/20261108T110000Z/" {
		t.Fatalf("rewrite sync = %+v", res)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != "refs/heads/feature" {
		t.Errorf("deleted = %v", res.Deleted)
	}

	local, _ := g.LocalRefs(ctx, mirror)
	if local["refs/heads/main"] != rewritten || local["refs/tags/v1.0"] != rewritten {
		t.Errorf("mirror not updated: %v", local)
	}
	if local["refs/heads/feature"] == "" {
		t.Error("branch deleted upstream was removed from the mirror")
	}
	backups, err := g.Backups(ctx, mirror)
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %+v, %v", backups, err)
	}
	if backups[0].Refs["refs/heads/main"] != second || backups[0].Refs["refs/tags/v1.0"] != first {
		t.Errorf("backup content = %v", backups[0].Refs)
	}
	// The old commit is still readable.
	if b, err := g.ReadFile(ctx, mirror, second, "a.txt", 0); err != nil || string(b) != "a" {
		t.Errorf("old history lost: %q %v", b, err)
	}

	// Backups can move to the trash namespace and be deleted there.
	n, err := g.MoveRefs(ctx, mirror, BackupNamespace, "refs/reposhelf/trash/x/")
	if err != nil || n != 2 {
		t.Fatalf("MoveRefs = %d, %v", n, err)
	}
	if b, _ := g.Backups(ctx, mirror); len(b) != 0 {
		t.Error("backup still listed after move")
	}
	if err := g.DeleteRefs(ctx, mirror, "refs/heads/"); err == nil {
		t.Error("DeleteRefs outside own namespace allowed")
	}
	if err := g.DeleteRefs(ctx, mirror, "refs/reposhelf/trash/x/"); err != nil {
		t.Error(err)
	}
}

func TestReadFunctions(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	up := newUpstream(t, g)
	os.MkdirAll(filepath.Join(up.dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(up.dir, "docs", "x.md"), []byte("doc"), 0o644)
	up.git("add", "docs")
	up.commit("ReadMe.md", "# Titel\n", "init")
	up.git("tag", "v0.1.0")

	mirror := filepath.Join(t.TempDir(), "m.git")
	g.InitBare(ctx, mirror, up.dir)
	if _, err := g.Mirror(ctx, mirror, up.dir, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !g.HasCommits(ctx, mirror) {
		t.Fatal("no commits")
	}
	tree, err := g.ListTree(ctx, mirror, "HEAD", "")
	if err != nil || len(tree) != 2 || tree[0].Name != "docs" || tree[0].Type != "tree" {
		t.Fatalf("ListTree = %+v, %v", tree, err)
	}
	sub, err := g.ListTree(ctx, mirror, "main", "/docs/../docs")
	if err != nil || len(sub) != 1 || sub[0].Path != "docs/x.md" {
		t.Fatalf("ListTree docs = %+v, %v", sub, err)
	}
	name, content, err := g.Readme(ctx, mirror, "HEAD", 1000)
	if err != nil || name != "ReadMe.md" || string(content) != "# Titel\n" {
		t.Errorf("Readme = %q %q %v", name, content, err)
	}
	if _, err := g.ReadFile(ctx, mirror, "HEAD", "ReadMe.md", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("limit = %v", err)
	}
	if _, err := g.ReadFile(ctx, mirror, "HEAD", "nope", 0); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing = %v", err)
	}
	for _, rev := range []string{"--output=/tmp/x", "", "a b", "HEAD:x"} {
		if _, err := g.ListTree(ctx, mirror, rev, ""); !errors.Is(err, ErrBadRev) {
			t.Errorf("rev %q accepted: %v", rev, err)
		}
	}
	log, err := g.Log(ctx, mirror, "HEAD", 5)
	if err != nil || len(log) != 1 || log[0].Subject != "init" {
		t.Errorf("Log = %+v, %v", log, err)
	}
	br, _ := g.Branches(ctx, mirror)
	vs, _ := g.Versions(ctx, mirror)
	if len(br) != 1 || len(vs) != 1 || vs[0].Name != "v0.1.0" {
		t.Errorf("Branches/Versions = %+v %+v", br, vs)
	}
	if DirSize(mirror) == 0 {
		t.Error("DirSize = 0")
	}
}

func TestErrorClassification(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	_, err := g.LsRemote(ctx, filepath.Join(t.TempDir(), "missing"), nil)
	if !errors.Is(err, ErrGone) {
		t.Errorf("missing repo = %v, want ErrGone", err)
	}
	cases := map[string]error{
		"fatal: Authentication failed for 'https://x'":              ErrAuth,
		"fatal: could not read Username: terminal prompts disabled": ErrAuth,
		"fatal: unable to access: Could not resolve host: x":        ErrUnreachable,
		"remote: Repository not found.":                             ErrGone,
	}
	for msg, want := range cases {
		if err := classify(&Error{Stderr: msg, Err: errors.New("exit 128")}, ctx); !errors.Is(err, want) {
			t.Errorf("%q → %v, want %v", msg, err, want)
		}
	}
	a := &Auth{Token: "secret", URLPrefix: "https://github.com/"}
	env := strings.Join(a.env(), " ")
	if !strings.Contains(env, "http.https://github.com/.extraHeader") || strings.Contains(env, "secret") {
		t.Errorf("auth env = %s", env)
	}
}

func TestNoForeignRepoConfig(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	up := newUpstream(t, g)
	up.commit("a", "a", "init")
	// A repository with a broken extra header, as left by a CI checkout.
	foreign := t.TempDir()
	if _, err := g.Run(ctx, "", nil, "init", "--quiet", foreign); err != nil {
		t.Fatal(err)
	}
	g.Run(ctx, foreign, nil, "config", "http.extraHeader", "Authorization: Basic broken")
	wd, _ := os.Getwd()
	if err := os.Chdir(foreign); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	out, err := g.Run(ctx, "", nil, "config", "--get", "http.extraHeader")
	if err == nil && strings.TrimSpace(string(out)) != "" {
		t.Errorf("foreign repository config leaked: %q", out)
	}
}

func TestMirrorReportsProgress(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	up := newUpstream(t, g)
	for i := range 30 {
		up.commit(fmt.Sprintf("f%d.txt", i), strings.Repeat("x", 4000+i), "c")
	}
	mirror := filepath.Join(t.TempDir(), "m.git")
	if err := g.InitBare(ctx, mirror, up.dir); err != nil {
		t.Fatal(err)
	}
	var seen []int
	pctx := WithProgress(ctx, func(p int) { seen = append(seen, p) })
	if _, err := g.Mirror(pctx, mirror, up.dir, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !g.HasCommits(ctx, mirror) {
		t.Fatal("mirror empty")
	}
	t.Logf("progress: %v", seen)
	for i := 1; i < len(seen); i++ {
		if seen[i] < 0 || seen[i] > 100 {
			t.Errorf("out of range: %v", seen)
		}
	}
	pw := &progressWriter{fn: func(p int) { seen = append(seen, p) }}
	seen = nil
	pw.Write([]byte("Receiving objects:  50% (5/10)\rReceiving objects: 100% (10/10), done.\nResolving deltas: 100% (3/3), done.\n"))
	if len(seen) != 3 || seen[0] != 50 || seen[1] != 90 || seen[2] != 100 {
		t.Errorf("parsed = %v", seen)
	}
}

// TestGitHardening: remote helpers like ext:: are refused, the data folder
// is a safe directory, credentials and base settings are combined (R-024).
func TestGitHardening(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	if _, err := g.LsRemote(ctx, "ext::sh -c touch% /tmp/reposhelf-pwned", nil); err == nil {
		t.Error("ext:: transport allowed")
	}
	if _, err := os.Stat("/tmp/reposhelf-pwned"); err == nil {
		t.Fatal("ext:: command ran")
	}
	out, err := g.Run(ctx, "", nil, "config", "--get", "safe.directory")
	if err != nil || strings.TrimSpace(string(out)) != "*" {
		t.Errorf("safe.directory = %q, %v", out, err)
	}
	a := &Auth{Token: "secret", URLPrefix: "https://example.org/"}
	cmd := g.command(ctx, "", a, "config", "--get-regexp", ".")
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "GIT_CONFIG_COUNT=2") || !strings.Contains(env, "GIT_CONFIG_KEY_1=http.https://example.org/.extraHeader") {
		t.Errorf("config env:\n%s", env)
	}
}
