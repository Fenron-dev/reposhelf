// SPDX-License-Identifier: GPL-3.0-or-later

// Package buildinfo carries version information that is injected at link time,
// e.g. go build -ldflags "-X github.com/Fenron-dev/reposhelf/internal/buildinfo.Version=v0.1.0".
// It has no dependencies so every layer may import it.
package buildinfo

// devVersion marks binaries built without release ldflags, so a local build
// is never mistaken for a published release.
const devVersion = "dev"

// Version is overwritten by the release build via -ldflags -X.
var Version = devVersion

// Commit is overwritten by the release build via -ldflags -X.
var Commit = ""

// String returns the version, followed by the short commit if known.
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
