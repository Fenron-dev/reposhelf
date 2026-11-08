// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin || linux

package fsinfo

import "golang.org/x/sys/unix"

// FreeBytes returns the space available to unprivileged users on the volume
// of path, or -1 if unknown.
func FreeBytes(path string) int64 {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
