// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// fakeTrash records instead of touching the real system trash.
func fakeTrash(t *testing.T) *[]string {
	t.Helper()
	var moved []string
	oldA, oldM := trashAvailable, trashMove
	trashAvailable = func(string) bool { return true }
	trashMove = func(p string) error {
		moved = append(moved, p)
		return os.RemoveAll(p) // stands in for the trash
	}
	t.Cleanup(func() { trashAvailable, trashMove = oldA, oldM })
	return &moved
}

func writeModel(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "voices"), 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"dim": 512}`), 0o644)
	os.WriteFile(filepath.Join(dir, "voices", "af heart.pt"), bytes.Repeat([]byte("v"), 2<<20), 0o644)
	os.WriteFile(filepath.Join(dir, "model.safetensors"), bytes.Repeat([]byte("w"), 3<<20), 0o644)
	os.MkdirAll(filepath.Join(dir, ".cache", "huggingface"), 0o755)
	os.WriteFile(filepath.Join(dir, ".cache", "huggingface", "x.lock"), []byte("x"), 0o644)
}

func TestIsLocalPath(t *testing.T) {
	for in, want := range map[string]bool{
		`C:\Modelle\kokoro`: true, `d:/x`: true, `\\nas\share\m`: true, "/Users/d/m": true, "~/m": true, "file:///tmp/m": true,
		"https://huggingface.co/a/b": false, "github.com/a/b": false, "git@github.com:a/b.git": false, "smb://nas/m": false,
	} {
		if IsLocalPath(in) != want {
			t.Errorf("IsLocalPath(%q) = %v", in, !want)
		}
	}
}

// TestImportLocalModel: a folder without a model ID becomes a local model
// repository; large files go to LFS. Copy keeps the original, move puts it
// into the (fake) trash after the check (D-032, D-033).
func TestImportLocalModel(t *testing.T) {
	ctx := context.Background()
	moved := fakeTrash(t)
	l, _ := openLib(t, testutil.NewForge(t))

	src := filepath.Join(t.TempDir(), "Mein Modell")
	writeModel(t, src)
	info, err := l.InspectFolder(src)
	if err != nil || info.Files != 3 || info.ModelID != "" || !info.CanMove {
		t.Fatalf("inspect = %+v, %v", info, err)
	}
	r, err := l.ImportFolder(ctx, ImportRequest{Path: src, Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Entry.Key != "local/modelle/Mein-Modell" || !r.R().Include.LFS {
		t.Errorf("repo = %s %+v", r.Entry.Key, r.R().Include)
	}
	attrs, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", ".gitattributes", 1<<20)
	if err != nil || !strings.Contains(string(attrs), "*.safetensors filter=lfs") || !strings.Contains(string(attrs), "*.pt filter=lfs") {
		t.Errorf(".gitattributes = %q, %v", attrs, err)
	}
	ptr, _ := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", "voices/af heart.pt", 1<<20)
	if !strings.HasPrefix(string(ptr), "version https://git-lfs") {
		t.Errorf("not an LFS pointer: %q", ptr)
	}
	if cfg, _ := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", "config.json", 1<<20); string(cfg) != `{"dim": 512}` {
		t.Errorf("config.json = %q", cfg)
	}
	if _, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", ".cache/huggingface/x.lock", 1<<20); err == nil {
		t.Error("download cache taken over")
	}
	if _, err := os.Stat(src); err != nil || len(*moved) != 0 {
		t.Error("copy touched the original")
	}

	// Move: second folder, same content (stored once), original to trash.
	src2 := filepath.Join(t.TempDir(), "kokoro")
	writeModel(t, src2)
	r2, err := l.ImportFolder(ctx, ImportRequest{Path: src2, Move: true, Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*moved) != 1 || (*moved)[0] != src2 {
		t.Errorf("trash = %v", *moved)
	}
	files, _ := folderFiles(src) // the first original is still there
	if missing, err := l.verifyImport(ctx, r2, files); err != nil || missing != 0 {
		t.Errorf("verify = %d, %v", missing, err)
	}

	// The check after a model's first sync: an extra file keeps the
	// original where it is; without it, it goes to the trash.
	os.WriteFile(filepath.Join(src, "meine-notiz.txt"), []byte("eigene Zusätze"), 0o644)
	r.Upstream.Import = &ImportState{From: src, Dir: src, Move: true}
	l.finishImport(ctx, r)
	if !r.Upstream.Import.Done || !strings.Contains(r.Upstream.Import.Note, "1 Dateien") || len(*moved) != 1 {
		t.Errorf("extra file: %+v, trash %v", r.Upstream.Import, *moved)
	}
	os.Remove(filepath.Join(src, "meine-notiz.txt"))
	r.Upstream.Import = &ImportState{From: src, Dir: src, Move: true}
	l.finishImport(ctx, r)
	if len(*moved) != 2 || !strings.Contains(r.Upstream.Import.Note, "Papierkorb") {
		t.Errorf("move after check: %+v, trash %v", r.Upstream.Import, *moved)
	}
}

// TestHFCacheFolder: the Hugging Face cache layout names the model and
// its snapshot (files are links into blobs/).
func TestHFCacheFolder(t *testing.T) {
	fakeTrash(t)
	l, _ := openLib(t, testutil.NewForge(t))
	root := filepath.Join(t.TempDir(), "hub", "models--hexgrad--Kokoro-82M")
	snap := filepath.Join(root, "snapshots", "abc123")
	os.MkdirAll(filepath.Join(root, "blobs"), 0o755)
	os.MkdirAll(filepath.Join(root, "refs"), 0o755)
	os.MkdirAll(snap, 0o755)
	os.WriteFile(filepath.Join(root, "refs", "main"), []byte("abc123"), 0o644)
	big := bytes.Repeat([]byte("k"), 2<<20)
	oid, _ := gitcli.SHA256(bytes.NewReader(big))
	os.WriteFile(filepath.Join(root, "blobs", oid), big, 0o644)
	if err := os.Symlink(filepath.Join("..", "..", "blobs", oid), filepath.Join(snap, "kokoro-v1_0.pth")); err != nil {
		t.Skip("no symlinks here")
	}
	for _, given := range []string{root, snap} {
		info, err := l.InspectFolder(given)
		if err != nil || info.ModelID != "hexgrad/Kokoro-82M" || info.Root != root || info.Dir != snap || info.Files != 1 {
			t.Errorf("inspect %s = %+v, %v", given, info, err)
		}
	}
	files, err := folderFiles(snap)
	if err != nil || len(files) != 1 || files[0].rel != "kokoro-v1_0.pth" {
		t.Fatalf("files = %+v, %v", files, err)
	}
	if !l.lfs {
		t.Skip("git-lfs not installed")
	}
	// The local file seeds the LFS store; the first sync would fetch only
	// what is missing (no network here: the sync is not started).
	r, err := l.ImportFolder(context.Background(), ImportRequest{Path: snap, Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !gitcli.HasLFS(r.Paths.Mirror, oid) || r.Upstream.Import == nil || r.Entry.Key != "huggingface.co/hexgrad/Kokoro-82M" {
		t.Errorf("seeded = %v, import %+v, key %s", gitcli.HasLFS(r.Paths.Mirror, oid), r.Upstream.Import, r.Entry.Key)
	}
}
