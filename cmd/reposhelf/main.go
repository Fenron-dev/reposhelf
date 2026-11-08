// SPDX-License-Identifier: GPL-3.0-or-later

// Command reposhelf is the single RepoShelf binary: CLI, server, MCP server and
// (later) runner role all live here (D-003). main only wires the process to
// the cli package so that all behaviour stays testable.
package main

import (
	"os"

	"github.com/Fenron-dev/reposhelf/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
