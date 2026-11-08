// SPDX-License-Identifier: GPL-3.0-or-later

package fsinfo

import "golang.org/x/sys/unix"

func fsType(path string) string {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return ""
	}
	return unix.ByteSliceToString(st.Fstypename[:])
}
