// SPDX-License-Identifier: GPL-3.0-or-later

package fsinfo

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func fsType(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	root := filepath.VolumeName(abs) + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	var name [windows.MAX_PATH + 1]uint16
	if windows.GetVolumeInformation(rootPtr, nil, 0, nil, nil, nil, &name[0], uint32(len(name))) != nil {
		return ""
	}
	return windows.UTF16ToString(name[:])
}
