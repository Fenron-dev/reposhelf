// SPDX-License-Identifier: GPL-3.0-or-later

// Package systrash moves a folder into the operating system's trash, so a
// user can restore it (D-032: "Verschieben" of an imported folder). It is
// the only place where RepoShelf removes files it did not write itself.
package systrash

import "errors"

// ErrUnsupported is returned where there is no system trash (servers,
// Docker, other volumes without a trash folder).
var ErrUnsupported = errors.New("kein Papierkorb des Systems verfügbar")

// Available reports whether path can be moved to the system trash.
func Available(path string) bool { return available(path) }

// Move puts path (file or folder) into the system trash.
func Move(path string) error { return move(path) }
