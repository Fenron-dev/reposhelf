// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package core

import (
	"errors"
	"syscall"
)

// isNoLinkErr: file systems without hard links answer EPERM/ENOTSUP/EXDEV.
func isNoLinkErr(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EXDEV)
}
