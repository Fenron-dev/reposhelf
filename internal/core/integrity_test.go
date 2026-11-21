// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestIntegrity: a clean archive passes; damaged release files, LFS
// objects and git objects are found and named (V-1).
func TestIntegrity(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("o", "tool")
	up.AddRelease("v1.0", "2026-10-01T00:00:00Z", "tool.zip", "ZIPDATA")
	l, _ := openLib(t, f)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "tool"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("m"), 1000)
	oid, _ := gitcli.SHA256(bytes.NewReader(content))
	gitcli.PutLFS(r.Paths.Mirror, oid, int64(len(content)), bytes.NewReader(content))

	if !l.IntegrityDue() {
		t.Error("first check not due")
	}
	rep, err := l.CheckIntegrity(ctx)
	if err != nil || !rep.OK() || rep.Repos != 1 || rep.Files < 2 {
		t.Fatalf("clean archive: %+v, %v", rep, err)
	}
	if l.IntegrityDue() {
		t.Error("due right after a check")
	}

	// Damage: one release file, one LFS object, one git object.
	rels, _ := l.Releases(ctx, r.R().ID)
	asset := filepath.Join(rels[0].Dir, rels[0].Stored[0].File)
	os.Chmod(asset, 0o644)
	os.WriteFile(asset, []byte("verändert"), 0o644)
	obj := gitcli.LFSObjectPath(r.Paths.Mirror, oid)
	os.Chmod(obj, 0o644)
	os.WriteFile(obj, []byte("kaputt"), 0o644)
	head := strings.TrimSpace(up.Git("rev-parse", "HEAD"))
	loose := filepath.Join(r.Paths.Mirror, "objects", head[:2], head[2:])
	if _, err := os.Stat(loose); err == nil {
		os.Chmod(loose, 0o644)
		os.Remove(loose)
	} else {
		// Packed: remove the pack instead.
		packs, _ := filepath.Glob(filepath.Join(r.Paths.Mirror, "objects", "pack", "*.pack"))
		for _, p := range packs {
			os.Chmod(p, 0o644)
			os.Remove(p)
		}
	}
	rep, err = l.CheckIntegrity(ctx)
	if err != nil || rep.OK() {
		t.Fatalf("damage not found: %+v, %v", rep, err)
	}
	kinds := map[string]bool{}
	for _, p := range rep.Problems {
		kinds[p.Kind] = true
		if p.RepoID != r.R().ID {
			t.Errorf("problem without repo: %+v", p)
		}
	}
	for _, k := range []string{"release", "lfs", "git"} {
		if !kinds[k] {
			t.Errorf("%s damage not reported: %+v", k, rep.Problems)
		}
	}
	if last, ok := l.LastIntegrity(); !ok || len(last.ProblemsOf(r.R().ID)) != len(rep.Problems) {
		t.Errorf("report not kept: %+v", last)
	}
	_ = time.Second
}

func TestFsckFilter(t *testing.T) {
	for line, want := range map[string]bool{
		"error in commit 1234: badTimezone: invalid author/committer line - bad time zone": false,
		"warning in tree 5678: zeroPaddedFilemode: contains zero-padded file modes":         false,
		"missing blob 0123456789abcdef":                                                     true,
		"error: sha1 mismatch for .git/objects/ab/cd":                                       true,
		"broken link from tree 1 to blob 2":                                                 true,
		"Checking object directories":                                                       false,
	} {
		got := fsckProblem.MatchString(line) && !fsckBenign.MatchString(line)
		if got != want {
			t.Errorf("%q = %v", line, got)
		}
	}
}
