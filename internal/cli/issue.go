// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// runIssue manages local issues (D-028, T-9.13):
//
//	issue list <repo> [--all]
//	issue new <repo> --title T [--body B | --body -] [--labels a,b]
//	issue comment <repo> L3 --body B
//	issue close|reopen <repo> L3 [--body B]
func runIssue(args []string, sys system) int {
	fset, data := dataFlagSet("issue", sys)
	title := fset.String("title", "", "")
	body := fset.String("body", "", "")
	labels := fset.String("labels", "", "")
	all := fset.Bool("all", false, "")
	pos, code, ok := parseArgs(fset, args, 2, 3, sys)
	if !ok {
		return code
	}
	sub, repo := pos[0], pos[1]
	if *body == "-" && sys.stdin != nil {
		b, _ := io.ReadAll(io.LimitReader(sys.stdin, 1<<20))
		*body = string(b)
	}
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	number := func() (int, bool) {
		if len(pos) < 3 {
			fmt.Fprintln(sys.stderr, "Issue fehlt, z. B. L3")
			return 0, false
		}
		n, err := core.ParseIssueRef(pos[2])
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return 0, false
		}
		return n, true
	}
	var err error
	switch sub {
	case "list", "ls":
		list, e := lib.Issues(ctx, repo)
		if e != nil {
			err = e
			break
		}
		for _, is := range list {
			if !*all && is.State != core.IssueOpen {
				continue
			}
			state := "offen"
			if is.State != core.IssueOpen {
				state = "zu"
			}
			fmt.Fprintf(sys.stdout, "%-5s %-5s %s", is.Ref(), state, is.Title)
			if len(is.Labels) > 0 {
				fmt.Fprintf(sys.stdout, " [%s]", strings.Join(is.Labels, ", "))
			}
			fmt.Fprintln(sys.stdout)
		}
	case "new":
		n, e := lib.CreateIssue(ctx, repo, *title, *body, splitList(*labels), "cli")
		if err = e; err == nil {
			fmt.Fprintf(sys.stdout, "Issue L%d angelegt.\n", n)
		}
	case "comment":
		n, ok := number()
		if !ok {
			return ExitUsage
		}
		if err = lib.CommentIssue(ctx, repo, n, *body, "cli"); err == nil {
			fmt.Fprintf(sys.stdout, "Kommentar an L%d gespeichert.\n", n)
		}
	case "close", "reopen":
		n, ok := number()
		if !ok {
			return ExitUsage
		}
		state := map[string]string{"close": core.IssueClosed, "reopen": core.IssueOpen}[sub]
		if err = lib.SetIssueState(ctx, repo, n, state, *body, "cli"); err == nil {
			fmt.Fprintf(sys.stdout, "L%d ist jetzt %s.\n", n, map[string]string{core.IssueClosed: "geschlossen", core.IssueOpen: "offen"}[state])
		}
	default:
		fmt.Fprintf(sys.stderr, msgUnknownCommand, "issue "+sub)
		return ExitUsage
	}
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	return ExitOK
}
