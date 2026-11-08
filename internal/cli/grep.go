// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

func init() { extraCommands["grep"] = runGrep }

// runGrep searches the code of all repositories (T-9.5, D-021).
func runGrep(args []string, sys system) int {
	fset, data := dataFlagSet("grep", sys)
	regex := fset.Bool("regex", false, "")
	icase := fset.Bool("i", false, "")
	tag := fset.String("tag", "", "")
	repo := fset.String("repo", "", "")
	limit := fset.Int("limit", 200, "")
	pos, code, ok := parseArgs(fset, args, 1, 1, sys)
	if !ok {
		return code
	}
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	res, err := lib.SearchCode(ctx, core.CodeQuery{Pattern: pos[0], Regex: *regex, IgnoreCase: *icase, Tag: *tag, Repo: *repo, Limit: *limit})
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	for _, h := range res.Hits {
		fmt.Fprintf(sys.stdout, "%s:%s:%d: %s\n", h.Key, h.Path, h.Line, h.Text)
	}
	note := ""
	if res.Truncated {
		note = " (gekürzt – Suche eingrenzen)"
	}
	fmt.Fprintf(sys.stderr, "%d Treffer in %d Repos%s\n", len(res.Hits), res.Repos, note)
	if len(res.Hits) == 0 {
		return ExitError
	}
	return ExitOK
}
