// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
)

// runCheck reads everything kept and verifies it (V-1, T-9.21). Exit code
// 1 when something is damaged or missing.
func runCheck(args []string, sys system) int {
	fset, data := dataFlagSet("check", sys)
	if _, code, ok := parseArgs(fset, args, 0, 0, sys); !ok {
		return code
	}
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	fmt.Fprintln(sys.stdout, "Prüfe alles, was RepoShelf aufbewahrt …")
	rep, err := lib.CheckIntegrity(ctx)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, "%d Repos, %d Dateien (%s) geprüft.\n", rep.Repos, rep.Files, humanSize(rep.Bytes))
	for _, p := range rep.Problems {
		who := p.Key
		if who == "" {
			who = "Kapseln"
		}
		fmt.Fprintf(sys.stdout, "! %s · %s (%s): %s\n", who, p.Path, p.Kind, p.Detail)
	}
	if rep.Error != "" {
		fmt.Fprintln(sys.stderr, rep.Error)
	}
	if !rep.OK() {
		fmt.Fprintf(sys.stdout, "%d Probleme gefunden.\n", len(rep.Problems))
		return ExitError
	}
	fmt.Fprintln(sys.stdout, "Alles in Ordnung.")
	return ExitOK
}
