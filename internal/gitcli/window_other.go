// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package gitcli

import "os/exec"

// HideWindow is only needed on Windows.
func HideWindow(*exec.Cmd) {}
