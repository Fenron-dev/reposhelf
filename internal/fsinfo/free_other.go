// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !darwin && !linux && !windows

package fsinfo

// FreeBytes is unknown on this platform.
func FreeBytes(string) int64 { return -1 }
