// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestZipWithForeignGitDir: a zipped clone keeps its history, but its own
// settings, hooks and alternates are not used (R-027).
func TestZipWithForeignGitDir(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	l, _ := openLib(t, f)
	up := f.NewRepo("o", "tool")
	up.Commit("a.txt", "hallo\n", "zweiter Stand")
	gitDir := filepath.Join(up.Work, ".git")
	os.WriteFile(filepath.Join(gitDir, "objects", "info", "alternates"), []byte("/etc\n"), 0o644)
	os.MkdirAll(filepath.Join(gitDir, "hooks"), 0o755)
	os.WriteFile(filepath.Join(gitDir, "hooks", "post-checkout"), []byte("#!/bin/sh\ntouch /tmp/reposhelf-hook\n"), 0o755)
	cfg, _ := os.ReadFile(filepath.Join(gitDir, "config"))
	os.WriteFile(filepath.Join(gitDir, "config"), append(cfg, []byte("[core]\n\tfsmonitor = touch /tmp/reposhelf-fsmonitor\n")...), 0o644)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	filepath.WalkDir(up.Work, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(up.Work, p)
		w, _ := zw.Create("tool/" + filepath.ToSlash(rel))
		b, _ := os.ReadFile(p)
		w.Write(b)
		return nil
	})
	zw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	r, err := l.ImportZIP(ctx, zr, "", "", "", nil, "t")
	if err != nil {
		t.Fatal(err)
	}
	log, err := l.Git.Log(ctx, r.Paths.Mirror, "HEAD", 5)
	if err != nil || len(log) != 2 {
		t.Errorf("history = %+v, %v", log, err)
	}
	for _, p := range []string{"/tmp/reposhelf-hook", "/tmp/reposhelf-fsmonitor"} {
		if _, err := os.Stat(p); err == nil {
			os.Remove(p)
			t.Errorf("foreign setting ran: %s", p)
		}
	}

	// The neutralised folder keeps only what git needs.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "objects", "info"), 0o755)
	os.WriteFile(filepath.Join(dir, "objects", "info", "alternates"), []byte("/x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "config"), []byte("[core]\n\tfsmonitor = evil\n[extensions]\n\tobjectformat = sha256\n"), 0o644)
	if err := neutralizeGitDir(ctx, l.Git, dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config"))
	if strings.Contains(string(b), "fsmonitor") || !strings.Contains(string(b), "objectformat = sha256") {
		t.Errorf("config = %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "objects", "info", "alternates")); err == nil {
		t.Error("alternates kept")
	}
}
