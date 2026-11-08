// SPDX-License-Identifier: GPL-3.0-or-later

// Package fsinfo tells which kind of file system a path lives on. RepoShelf
// needs it for SQLite: write-ahead logging (WAL) relies on shared memory and
// locking that exFAT/FAT drivers – typical for USB drives – do not provide
// reliably (KONZEPT 5.3, T-0.9).
package fsinfo

import "strings"

// unsafeForWAL lists file system names (lower case) on which SQLite must use
// journal_mode=DELETE. fuseblk is included because on Linux exFAT and NTFS
// drivers often run through FUSE, and the name does not tell which.
var unsafeForWAL = map[string]bool{
	"exfat": true, "msdos": true, "vfat": true, "fat": true, "fat32": true,
	"fat16": true, "fuseblk": true, "smbfs": true, "cifs": true, "nfs": true,
	"webdav": true,
}

// Type returns the file system name of the volume holding path, in lower
// case, or "" if it cannot be determined.
func Type(path string) string {
	return strings.ToLower(fsType(path))
}

// SupportsWAL reports whether SQLite's WAL mode is safe on the volume of
// path. Unknown file systems count as safe: WAL is SQLite's normal mode on
// local disks.
func SupportsWAL(path string) bool {
	return !unsafeForWAL[Type(path)]
}
