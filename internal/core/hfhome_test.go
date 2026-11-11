// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestExportHF: a model is laid out like the Hugging Face cache; large
// files are read-only hard links to the archived objects (T-9.11).
func TestExportHF(t *testing.T) {
	ctx := context.Background()
	l, _ := openLib(t, testutil.NewForge(t))
	src := filepath.Join(t.TempDir(), "kokoro")
	writeModel(t, src)
	r, err := l.ImportFolder(ctx, ImportRequest{Path: src, Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	// Pretend it came from the Hub (the export only serves Hub models).
	r.R().Source = repometa.Source{Host: "huggingface.co", Owner: "hexgrad", Name: "Kokoro-82M"}

	if missing, err := l.exportHF(ctx, r); err != nil || missing != 0 {
		t.Fatalf("export = %d, %v", missing, err)
	}
	dir := filepath.Join(l.HFHome(), "hub", "models--hexgrad--Kokoro-82M")
	ref, err := os.ReadFile(filepath.Join(dir, "refs", "main"))
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snapshots", strings.TrimSpace(string(ref)))
	if b, _ := os.ReadFile(filepath.Join(snap, "config.json")); string(b) != `{"dim": 512}` {
		t.Errorf("config.json = %q", b)
	}
	big := filepath.Join(snap, "voices", "af heart.pt")
	st, err := os.Stat(big)
	if err != nil || st.Size() != 2<<20 || st.Mode().Perm()&0o222 != 0 {
		t.Fatalf("large file = %v, %v", st, err)
	}
	orig, _ := os.Stat(filepath.Join(src, "voices", "af heart.pt"))
	if os.SameFile(st, orig) {
		t.Error("export links to the user's original instead of the archive")
	}
	// Same revision again: nothing to do; usage is reported.
	if _, err := l.exportHF(ctx, r); err != nil {
		t.Fatal(err)
	}
	if u, ok := l.HFUsageOf(r); !ok || !u.Ready || u.Home != l.HFHome() {
		t.Errorf("usage = %+v", u)
	}
	// A new revision replaces the old snapshot.
	os.WriteFile(filepath.Join(src, "config.json"), []byte(`{"dim": 1024}`), 0o644)
	files, _ := folderFiles(src)
	if err := l.commitFolder(ctx, r, files, false, "update"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.exportHF(ctx, r); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "snapshots")); len(entries) != 1 {
		t.Errorf("snapshots = %d", len(entries))
	}
	if err := removeTree(dir); err != nil {
		t.Errorf("remove read-only export: %v", err)
	}
}
