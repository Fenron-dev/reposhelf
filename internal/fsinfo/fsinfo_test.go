// SPDX-License-Identifier: GPL-3.0-or-later

package fsinfo

import "testing"

func TestTypeOfTempDir(t *testing.T) {
	// The temp folder is on the system disk: known type, WAL allowed.
	dir := t.TempDir()
	if got := Type(dir); got == "" {
		t.Log("file system type unknown on this platform")
	}
	if !SupportsWAL(dir) {
		t.Errorf("WAL refused on system disk (%q)", Type(dir))
	}
}

func TestUnsafeNames(t *testing.T) {
	for _, n := range []string{"exfat", "msdos", "fuseblk"} {
		if !unsafeForWAL[n] {
			t.Errorf("%s should disable WAL", n)
		}
	}
	if Type("/does/not/exist/at/all") != "" {
		t.Error("missing path should give empty type")
	}
}
