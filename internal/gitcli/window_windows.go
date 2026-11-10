// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps console programs (git, docker, act) from opening a
// console window when started by the GUI app (T-9.8).
const createNoWindow = 0x08000000

// HideWindow starts cmd without a visible console window.
func HideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
