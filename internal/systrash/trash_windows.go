// SPDX-License-Identifier: GPL-3.0-or-later

package systrash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// The recycle bin is reached through .NET (no cgo, no extra DLL calls).
// The path goes in through an environment variable: arguments after
// -Command would become part of the script text (spaces split it, quotes
// could inject code).
const recycleScript = `$ErrorActionPreference = 'Stop'; $p = $env:REPOSHELF_TRASH_PATH; ` +
	`Add-Type -AssemblyName Microsoft.VisualBasic; ` +
	`if (Test-Path -LiteralPath $p -PathType Container) { [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteDirectory($p,'OnlyErrorDialogs','SendToRecycleBin') } ` +
	`elseif (Test-Path -LiteralPath $p) { [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($p,'OnlyErrorDialogs','SendToRecycleBin') } ` +
	`else { throw "not found: $p" }`

func available(path string) bool {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return false
	}
	// Network shares have no recycle bin: files would be deleted for good.
	return !strings.HasPrefix(path, `\\`)
}

func move(path string) error {
	if !available(path) {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", recycleScript)
	cmd.Env = append(os.Environ(), "REPOSHELF_TRASH_PATH="+path)
	gitcli.HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("papierkorb: %v %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("papierkorb: %s ist noch da", path)
	}
	return nil
}
