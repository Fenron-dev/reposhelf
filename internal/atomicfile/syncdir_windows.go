// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package atomicfile

// syncDir does nothing on Windows: directories cannot be opened for flushing
// there, and NTFS journals the rename itself.
func syncDir(string) error { return nil }
