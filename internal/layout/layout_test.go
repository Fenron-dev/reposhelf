// SPDX-License-Identifier: GPL-3.0-or-later

package layout

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

func TestInitAndPaths(t *testing.T) {
	root := t.TempDir()
	l, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range skeleton {
		if info, err := os.Stat(filepath.Join(root, d)); err != nil || !info.IsDir() {
			t.Errorf("%s missing", d)
		}
	}
	if _, err := Init(root); err != nil {
		t.Errorf("second Init: %v", err)
	}
	if _, err := Init(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Init of missing folder = %v", err)
	}

	src := repometa.Source{Host: "gitea.local:3000", Owner: "group/sub", Name: "Fero"}
	meta, _ := l.MetaDir(src)
	if want := filepath.Join(root, "meta", "gitea.local", "group", "sub", "Fero"); meta != want {
		t.Errorf("MetaDir = %s", meta)
	}
	mirror, _ := l.MirrorDir(src)
	if want := filepath.Join(root, "repos", "gitea.local", "group", "sub", "Fero.git"); mirror != want {
		t.Errorf("MirrorDir = %s", mirror)
	}
	rf, _ := l.RepoFile(src)
	if filepath.Base(rf) != repometa.FileName {
		t.Errorf("RepoFile = %s", rf)
	}
	if _, err := l.ReleasesDir(repometa.Source{Host: "h", Owner: "..", Name: "x"}); !errors.Is(err, safepath.ErrInvalid) {
		t.Errorf("unsafe owner accepted: %v", err)
	}
	if _, err := l.RepoFile(repometa.Source{Host: "h", Owner: "o", Name: "CON"}); err == nil {
		t.Error("reserved name accepted")
	}
	if l.IndexPath() != filepath.Join(root, ".reposhelf", "index.db") || l.LocksDir() == "" || l.LogsDir() == "" || l.TrashDir() == "" {
		t.Error("fixed paths wrong")
	}
}

func TestRepoFiles(t *testing.T) {
	l, _ := Init(t.TempDir())
	if files, err := (Layout{Root: t.TempDir()}).RepoFiles(); err != nil || len(files) != 0 {
		t.Errorf("no meta folder: %v %v", files, err)
	}
	for _, d := range []string{"meta/github.com/a/b", "meta/gitea.local/g/s/c"} {
		p := filepath.Join(l.Root, d)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, repometa.FileName), []byte("x"), 0o644)
	}
	os.WriteFile(filepath.Join(l.Root, "meta", "github.com", "a", "b", "notes.md"), nil, 0o644)
	files, err := l.RepoFiles()
	if err != nil || len(files) != 2 {
		t.Fatalf("RepoFiles = %v, %v", files, err)
	}
}

func TestCleanTemp(t *testing.T) {
	l, _ := Init(t.TempDir())
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	mk := func(rel string, mod time.Time) string {
		p := filepath.Join(l.Root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mod, mod)
		return p
	}
	stale := mk("meta/h/o/n/"+atomicfile.TempPrefix+"1", old)
	fresh := mk("meta/h/o/n/"+atomicfile.TempPrefix+"2", now)
	normal := mk("meta/h/o/n/repo.yaml", old)
	inGit := mk("repos/h/o/n.git/"+atomicfile.TempPrefix+"3", old)

	removed, err := l.CleanTemp(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != stale {
		t.Errorf("removed = %v", removed)
	}
	for _, p := range []string{fresh, normal, inGit} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
}
