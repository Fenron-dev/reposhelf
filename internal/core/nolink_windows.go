// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isNoLinkErr: FAT/exFAT answer ERROR_INVALID_FUNCTION or "not supported".
func isNoLinkErr(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
