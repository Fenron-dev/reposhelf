// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !desktop

// Command reposhelf-desktop is the desktop shell (Wails v2, D-019). Without
// the "desktop" build tag it only explains how to build it, so that
// "go build ./..." works on every machine without CGO and WebKit.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Die Desktop-App wird mit Wails gebaut: cd cmd/reposhelf-desktop && wails build")
	os.Exit(2)
}
