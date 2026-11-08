// SPDX-License-Identifier: GPL-3.0-or-later

package fsinfo

import "golang.org/x/sys/unix"

// Magic numbers from linux/magic.h and the exFAT driver.
const (
	magicMSDOS   = 0x4d44
	magicExFAT   = 0x2011bab0
	magicFUSE    = 0x65735546
	magicSMB     = 0x517b
	magicCIFS    = 0xff534d42
	magicSMB2    = 0xfe534d42
	magicNFS     = 0x6969
	magicEXT4    = 0xef53
	magicBTRFS   = 0x9123683e
	magicXFS     = 0x58465342
	magicTMPFS   = 0x01021994
	magicOverlay = 0x794c7630
	magicZFS     = 0x2fc12fc1
)

var names = map[int64]string{
	magicMSDOS: "msdos", magicExFAT: "exfat", magicFUSE: "fuseblk",
	magicSMB: "smbfs", magicCIFS: "cifs", magicSMB2: "cifs", magicNFS: "nfs",
	magicEXT4: "ext4", magicBTRFS: "btrfs", magicXFS: "xfs", magicTMPFS: "tmpfs",
	magicOverlay: "overlay", magicZFS: "zfs",
}

func fsType(path string) string {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return ""
	}
	return names[int64(st.Type)]
}
