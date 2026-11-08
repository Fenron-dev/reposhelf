// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"syscall"
)

// syncDir flushes a directory so that a rename inside it is durable.
//
// Some file systems (network mounts, FAT/exFAT drivers) cannot flush a
// directory and say so with EINVAL, ENOTSUP or ENOTTY. The rename has still
// happened then; durability is simply what that file system offers.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	err = d.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOTTY) {
		return nil
	}
	return err
}
