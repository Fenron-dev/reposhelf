// SPDX-License-Identifier: GPL-3.0-or-later

package systrash

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMove uses the real system trash; it only runs when
// REPOSHELF_TRASH_TEST=1 (manual check). The item stays in the trash.
func TestMove(t *testing.T) {
	if os.Getenv("REPOSHELF_TRASH_TEST") != "1" {
		t.Skip("set REPOSHELF_TRASH_TEST=1")
	}
	dir := filepath.Join(t.TempDir(), "reposhelf-trash-probe")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("x"), 0o444)
	if !Available(dir) {
		t.Fatal("no trash")
	}
	if err := Move(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("still there: %v", err)
	}
}
