// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli dispatches the reposhelf command line. It is a thin surface over
// core: it parses arguments, calls into the core packages and maps results to
// output and exit codes. Output goes through the given writers so the whole
// command line can be tested without a real terminal.
package cli

import (
	"fmt"
	"io"

	"github.com/Fenron-dev/reposhelf/internal/buildinfo"
)

// Exit codes follow the common shell convention: 2 means wrong usage.
const (
	ExitOK    = 0
	ExitUsage = 2
)

// Run executes the command given in args (without the program name) and
// returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, msgUsage)
		return ExitOK
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, msgUsage)
		return ExitOK
	case "version", "--version":
		fmt.Fprintf(stdout, msgVersion, buildinfo.String())
		return ExitOK
	default:
		fmt.Fprintf(stderr, msgUnknownCommand, args[0])
		fmt.Fprint(stderr, msgUsage)
		return ExitUsage
	}
}
