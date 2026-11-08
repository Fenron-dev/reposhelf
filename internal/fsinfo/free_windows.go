// SPDX-License-Identifier: GPL-3.0-or-later

package fsinfo

import "golang.org/x/sys/windows"

// FreeBytes returns the space available on the volume of path, or -1.
func FreeBytes(path string) int64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	var free, total, totalFree uint64
	if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) != nil {
		return -1
	}
	return int64(free)
}
