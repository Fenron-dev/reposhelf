// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// runHostToken manages host tokens in the system keychain (T-9.10). The token
// is read from standard input, never from the command line, so it does not
// end up in the shell history or the process list.
func runHostToken(args []string, sys system) int {
	fset, data := dataFlagSet("host-token", sys)
	pos, code, ok := parseArgs(fset, args, 1, 2, sys)
	if !ok {
		return code
	}
	sub, host := pos[0], "github.com"
	if len(pos) == 2 {
		host = strings.ToLower(pos[1])
	}
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	switch sub {
	case "list":
		hosts, err := lib.TokenHosts(ctx)
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		for _, h := range hosts {
			fmt.Fprintf(sys.stdout, "%-24s %3d Repos  %s\n", h.Host, h.Repos, sourceText(h.Source))
		}
		if !lib.Tokens.CanSave() {
			fmt.Fprintln(sys.stdout, "(kein Schlüsselbund – Tokens nur über Umgebungsvariablen)")
		}
	case "set":
		if sys.stdin == nil {
			fmt.Fprintln(sys.stderr, "Token bitte über die Eingabe übergeben")
			return ExitUsage
		}
		line, _ := bufio.NewReader(sys.stdin).ReadString('\n')
		acc, err := lib.SaveToken(ctx, host, line, "cli")
		if err != nil {
			fmt.Fprintf(sys.stderr, "Token für %s nicht gespeichert: %v\n", host, err)
			return ExitError
		}
		fmt.Fprintf(sys.stdout, "Token für %s im Schlüsselbund gespeichert. %s\n", host, accountLine(acc))
	case "check":
		acc, err := lib.CheckToken(ctx, host)
		if err != nil {
			fmt.Fprintf(sys.stderr, "Token für %s: %v\n", host, err)
			return ExitError
		}
		fmt.Fprintf(sys.stdout, "Token für %s (%s) gültig. %s\n", host, sourceText(lib.Tokens.Source(host)), accountLine(acc))
	case "remove":
		if err := lib.RemoveToken(host, "cli"); err != nil {
			fmt.Fprintf(sys.stderr, "Token für %s nicht entfernt: %v\n", host, err)
			return ExitError
		}
		fmt.Fprintf(sys.stdout, "Token für %s aus dem Schlüsselbund entfernt.\n", host)
		if src := lib.Tokens.Source(host); src.Kind != sources.FromNone {
			fmt.Fprintf(sys.stdout, "Es gilt weiter: %s\n", sourceText(src))
		}
	default:
		fmt.Fprintf(sys.stderr, msgUnknownCommand, "host-token "+sub)
		return ExitUsage
	}
	return ExitOK
}

func sourceText(s sources.TokenSource) string {
	switch s.Kind {
	case sources.FromKeychain:
		return "Schlüsselbund"
	case sources.FromNone:
		return "kein Token"
	}
	return s.Name
}

func accountLine(a sources.Account) string {
	if !a.Checked {
		return "(nicht geprüft)"
	}
	s := "Angemeldet als " + a.Login
	if a.Scopes != "" {
		s += ", Rechte: " + a.Scopes
	}
	if a.Private {
		s += ", private Repos sichtbar"
	}
	return s + "."
}
