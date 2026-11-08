// SPDX-License-Identifier: GPL-3.0-or-later

package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertNoTemp fails if a temporary file was left in dir.
func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if IsTemp(e.Name()) {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteFileCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.yaml")

	if err := WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := readFile(t, path); got != "first\n" {
		t.Errorf("after create = %q", got)
	}
	if err := WriteFile(path, []byte("second, longer\n"), 0o644); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got := readFile(t, path); got != "second, longer\n" {
		t.Errorf("after replace = %q", got)
	}
	if err := WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := readFile(t, path); got != "" {
		t.Errorf("after empty write = %q", got)
	}
	assertNoTemp(t, dir)
}

func TestWriteFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has only a read-only bit")
	}
	dir := t.TempDir()
	for _, perm := range []fs.FileMode{0o600, 0o644} {
		path := filepath.Join(dir, perm.String())
		if err := WriteFile(path, []byte("x"), perm); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != perm {
			t.Errorf("mode = %v, want %v", got, perm)
		}
	}
}

func TestWriteFailureKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.yaml")
	if err := WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := Write(path, 0o644, func(w io.Writer) error {
		if _, err := w.Write([]byte("half of the new")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := readFile(t, path); got != "original" {
		t.Errorf("target changed to %q", got)
	}
	assertNoTemp(t, dir)
}

func TestWriteMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-there", "repo.yaml")
	err := WriteFile(path, []byte("x"), 0o644)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
		t.Error("Write created the missing directory")
	}
}

func TestWriteOntoDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, []byte("x"), 0o644); err == nil {
		t.Fatal("replacing a directory succeeded")
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Error("directory was touched")
	}
	assertNoTemp(t, dir)
}

func TestWriteRejectsNonFilePaths(t *testing.T) {
	sep := string(filepath.Separator)
	for _, p := range []string{"", ".", "..", sep, "dir" + sep, "dir/", "a" + sep + ".."} {
		if err := WriteFile(p, []byte("x"), 0o644); !errors.Is(err, ErrNoFileName) {
			t.Errorf("WriteFile(%q) = %v, want ErrNoFileName", p, err)
		}
	}
}

func TestWriteThroughSymlinkKeepsLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "elsewhere", "reposhelf.yaml")
	if err := os.Mkdir(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "reposhelf.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	if err := WriteFile(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Error("link was replaced by a file")
	}
	if got := readFile(t, real); got != "new" {
		t.Errorf("link target = %q, want new", got)
	}
	assertNoTemp(t, dir)
	assertNoTemp(t, filepath.Dir(real))
}

func TestWriteThroughDanglingSymlinkFails(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "state.yaml")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}
	if err := WriteFile(link, []byte("x"), 0o644); err == nil {
		t.Fatal("write through dangling link succeeded")
	}
}

// TestConcurrentReadersSeeWholeFiles is the core promise: while the file is
// rewritten over and over, every read returns one complete version.
func TestConcurrentReadersSeeWholeFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repo.yaml")
	versions := [][]byte{
		bytes.Repeat([]byte("a"), 64*1024),
		bytes.Repeat([]byte("b"), 3),
	}
	if err := WriteFile(path, versions[0], 0o644); err != nil {
		t.Fatal(err)
	}

	const rounds = 200
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := range rounds {
			if err := WriteFile(path, versions[i%2], 0o644); err != nil {
				t.Errorf("write %d: %v", i, err)
				return
			}
		}
	}()

	for reading := true; reading; {
		select {
		case <-done:
			reading = false
		default:
		}
		b, err := os.ReadFile(path)
		if err != nil {
			// Windows may refuse to open a file during the rename; that is
			// not a torn read.
			if runtime.GOOS == "windows" {
				continue
			}
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(b, versions[0]) && !bytes.Equal(b, versions[1]) {
			t.Fatalf("torn read: %d bytes", len(b))
		}
	}
	wg.Wait()
	assertNoTemp(t, dir)
}

func TestIsTemp(t *testing.T) {
	if !IsTemp(TempPrefix + "123") {
		t.Error("own temporary name not recognised")
	}
	for _, name := range []string{"repo.yaml", ".reposhelf", "x" + TempPrefix} {
		if IsTemp(name) {
			t.Errorf("IsTemp(%q) = true", name)
		}
	}
	if !strings.HasPrefix(TempPrefix, ".") {
		t.Error("temporary files should be hidden on Unix")
	}
}
