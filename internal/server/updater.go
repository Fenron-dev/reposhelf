// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Fenron-dev/reposhelf/internal/buildinfo"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/update"
)

// NewUpdater prepares the self-update for this program (V-15, D-036).
// desktop is true in the desktop app. The caller starts its Run loop.
func NewUpdater(lib *core.Library, desktop bool, lookupEnv func(string) (string, bool)) *update.Updater {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	u := &update.Updater{
		Current:   buildinfo.Version,
		Flavor:    update.DetectFlavor(buildinfo.Version, desktop, update.InDocker(lookupEnv)),
		StatePath: lib.Layout.Path(layout.DirInternal, update.StateFile),
		Enabled:   func() bool { return lib.Config().Updates.Check },
		Now:       lib.Now,
		OnNew: func(r update.Release) {
			lib.Notify(core.Notification{Kind: core.NotifyUpdate, Title: "Neue RepoShelf-Version " + r.Tag,
				Text: "Installieren unter Einstellungen → Aktualisierung."})
		},
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		u.Exe = exe
	}
	if u.Flavor == update.FlavorMacApp && runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			u.DownloadDir = filepath.Join(home, "Downloads")
		}
		u.Open = func(p string) error { return exec.Command("open", p).Run() }
	}
	return u
}
