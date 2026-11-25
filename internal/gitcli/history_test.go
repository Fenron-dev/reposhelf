// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistory(t *testing.T) {
	ctx := context.Background()
	g := testGit(t)
	up := newUpstream(t, g)
	root := up.commit("a.txt", "eins\nzwei\ndrei\n", "init")
	up.git("tag", "v1.0")
	up.commit("a.txt", "eins\nZWEI\ndrei\nvier\n", "edit a\n\nWarum: Test.")
	up.git("mv", "a.txt", "b.txt")
	os.WriteFile(filepath.Join(up.dir, "mit leer.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(up.dir, "bild.bin"), []byte{0, 1, 2, 0, 3}, 0o644)
	up.git("add", ".")
	up.git("commit", "--quiet", "-m", "rename and add")
	head := up.git("rev-parse", "HEAD")

	// Pages of the log.
	all, err := g.LogPage(ctx, up.dir, "HEAD", 0, 10)
	if err != nil || len(all) != 3 || all[0].Hash != head {
		t.Fatalf("log %+v %v", all, err)
	}
	page, _ := g.LogPage(ctx, up.dir, "HEAD", 1, 1)
	if len(page) != 1 || page[0].Subject != "edit a" {
		t.Errorf("page 2 = %+v", page)
	}
	rng, _ := g.LogRange(ctx, up.dir, "v1.0", "HEAD", 10)
	if len(rng) != 2 {
		t.Errorf("range = %d commits", len(rng))
	}

	// One commit.
	c, err := g.CommitDetail(ctx, up.dir, "HEAD~1")
	if err != nil || c.Subject != "edit a" || c.Body != "Warum: Test." || len(c.Parents) != 1 || c.Parents[0] != root {
		t.Errorf("detail %+v %v", c, err)
	}

	// Diff of the root commit.
	d, err := g.Diff(ctx, up.dir, "", root, 1<<20)
	if err != nil || len(d.Files) != 1 || d.Files[0].Status != "added" || d.Adds != 3 {
		t.Fatalf("root diff %+v %v", d, err)
	}

	// Compare a version with HEAD: rename, new file with a space, binary.
	d, err = g.Diff(ctx, up.dir, "v1.0", "HEAD", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]FileDiff{}
	for _, f := range d.Files {
		files[f.Path()] = f
	}
	if f := files["b.txt"]; f.Status != "renamed" || f.OldPath != "a.txt" || f.Adds != 2 || f.Dels != 1 || !f.Shown {
		t.Errorf("rename %+v", f)
	}
	if f := files["mit leer.txt"]; f.Status != "added" || len(f.Hunks) != 1 {
		t.Errorf("space name %+v", f)
	}
	if f := files["bild.bin"]; !f.Binary || f.Status != "added" {
		t.Errorf("binary %+v", f)
	}
	var lines []string
	for _, l := range files["b.txt"].Hunks[0].Lines {
		lines = append(lines, string(l.Kind)+l.Text)
	}
	if got := strings.Join(lines, "|"); got != " eins|-zwei|+ZWEI| drei|+vier" {
		t.Errorf("lines %s", got)
	}
	if l := files["b.txt"].Hunks[0].Lines[4]; l.New != 4 || l.Old != 0 {
		t.Errorf("numbers %+v", l)
	}

	// A small limit cuts the patch; counts stay complete.
	d, err = g.Diff(ctx, up.dir, "v1.0", "HEAD", 40)
	if err != nil || !d.Truncated || len(d.Files) != 3 || d.Adds != files["b.txt"].Adds+files["mit leer.txt"].Adds {
		t.Errorf("truncated %+v %v", d, err)
	}

	// Options are no revisions.
	if _, err := g.Diff(ctx, up.dir, "--output=/tmp/x", "HEAD", 100); err == nil {
		t.Error("option accepted as revision")
	}
}
