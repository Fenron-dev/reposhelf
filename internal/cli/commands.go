// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
	"github.com/Fenron-dev/reposhelf/internal/fsinfo"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// cmdContext is a context cancelled by Ctrl+C.
func cmdContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// openLibrary resolves the data folder and opens it.
func openLibrary(ctx context.Context, dataFlag string, sys system) (*core.Library, int, bool) {
	loc, code, ok := resolveDataDir(dataFlag, sys)
	if !ok {
		return nil, code, false
	}
	exeDir := ""
	if exe, err := sys.executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	lib, err := core.Open(ctx, core.Options{DataDir: loc.Path, ExeDir: exeDir, LookupEnv: sys.lookupEnv, Git: sys.git, Keychain: sys.keychain})
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return nil, ExitError, false
	}
	return lib, ExitOK, true
}

// errText turns errors into German messages for the terminal.
func errText(err error) string {
	switch {
	case errors.Is(err, core.ErrNoGit):
		return fmt.Sprintf(msgNoGit, err)
	case errors.Is(err, core.ErrExists):
		return fmt.Sprintf("Schon vorhanden: %v", err)
	case errors.Is(err, core.ErrNotFound):
		return fmt.Sprintf("Nicht gefunden: %v", err)
	case errors.Is(err, core.ErrInvalid), errors.Is(err, sources.ErrBadURL):
		return fmt.Sprintf("Ungültige Angabe: %v", err)
	case errors.Is(err, gitcli.ErrAuth), errors.Is(err, sources.ErrAuth):
		return fmt.Sprintf("Zugriff verweigert – fehlt ein Token? %v", err)
	case errors.Is(err, sources.ErrRateLimited):
		return fmt.Sprintf("API-Limit erreicht – ein Token erhöht es auf 5000/Stunde. %v", err)
	case errors.Is(err, gitcli.ErrUnreachable):
		return fmt.Sprintf("Nicht erreichbar: %v", err)
	}
	return "Fehler: " + err.Error()
}

// dataFlagSet returns a flag set with the common --data option.
func dataFlagSet(name string, sys system) (*flag.FlagSet, *string) {
	fset := newFlagSet(name, sys)
	return fset, fset.String("data", "", "")
}

// parseArgs parses flags and allows positional arguments (between min and
// max; max < 0 means unlimited).
func parseArgs(fset *flag.FlagSet, args []string, min, max int, sys system) ([]string, int, bool) {
	// Allow flags after positional arguments ("add URL --tags x").
	var pos []string
	rest := args
	for {
		if err := fset.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, ExitOK, false
			}
			return nil, ExitUsage, false
		}
		rest = fset.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		fmt.Fprint(sys.stderr, msgUsage)
		return nil, ExitUsage, false
	}
	return pos, ExitOK, true
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- init (T-0.12) ----

func runInit(args []string, sys system) int {
	fset := newFlagSet("init", sys)
	noPointer := fset.Bool("no-pointer", false, "")
	pos, code, ok := parseArgs(fset, args, 0, 1, sys)
	if !ok {
		return code
	}
	var dir string
	portable := false
	if len(pos) == 1 {
		abs, err := filepath.Abs(pos[0])
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		dir = abs
	} else {
		exe, err := sys.executable()
		if err != nil {
			fmt.Fprint(sys.stderr, msgInitNeedsDir)
			return ExitUsage
		}
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir = filepath.Join(datadir.PortableBase(exe), datadir.PortableDirName)
		portable = true
	}
	// The user named this folder explicitly, so creating it is not a silent
	// choice of location (invariant 5).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	if _, err := layout.Init(dir); err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgInitDone, dir)
	if fsinfo.Type(dir) != "" && !fsinfo.SupportsWAL(dir) {
		fmt.Fprintf(sys.stdout, msgInitFAT, fsinfo.Type(dir))
	}
	if portable {
		fmt.Fprint(sys.stdout, msgInitPortable)
		return ExitOK
	}
	if *noPointer {
		fmt.Fprintf(sys.stdout, msgInitNoPointer, dir)
		return ExitOK
	}
	cfgDir, err := sys.userConfigDir()
	if err != nil {
		fmt.Fprintf(sys.stdout, msgInitNoPointer, dir)
		return ExitOK
	}
	if err := datadir.WritePointer(cfgDir, dir); err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgInitPointer, datadir.PointerPath(cfgDir))
	return ExitOK
}

// ---- add / create / fork ----

func runAdd(args []string, sys system) int {
	fset, data := dataFlagSet("add", sys)
	kind := fset.String("kind", "mirror", "")
	mode := fset.String("mode", "follow", "")
	interval := fset.String("interval", "", "")
	tags := fset.String("tags", "", "")
	coll := fset.String("collection", "", "")
	buildable := fset.Bool("buildable", false, "")
	noReleases := fset.Bool("no-releases", false, "")
	issues := fset.Bool("issues", false, "")
	wiki := fset.Bool("wiki", false, "")
	actions := fset.Bool("actions", false, "")
	large := fset.Bool("large", false, "")
	model := fset.String("model", "", "")
	move := fset.Bool("move", false, "")
	pos, code, ok := parseArgs(fset, args, 1, -1, sys)
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
	exit := ExitOK
	for _, url := range pos {
		if core.IsLocalPath(url) {
			fmt.Fprintf(sys.stdout, msgAdding, url)
			r, err := lib.ImportFolder(ctx, core.ImportRequest{Path: url, ModelID: *model, Move: *move, Tags: splitList(*tags), Actor: "cli", Wait: true})
			if err != nil {
				fmt.Fprintln(sys.stderr, errText(err))
				exit = ExitError
				if r == nil {
					continue
				}
			}
			if r, _ = lib.Get(ctx, r.R().ID); r.Upstream.Import != nil && r.Upstream.Import.Note != "" {
				fmt.Fprintln(sys.stdout, r.Upstream.Import.Note)
			}
			fmt.Fprintf(sys.stdout, msgAdded, r.Entry.Key, statusLabel(r.Status()), humanSize(r.Upstream.Stats.SizeBytes))
			continue
		}
		req := core.AddRequest{URL: url, Kind: repometa.Kind(*kind), Mode: repometa.SyncMode(*mode),
			Tags: splitList(*tags), Collections: splitList(*coll), Buildable: *buildable, Actor: "cli", Wait: true, ConfirmLarge: *large}
		if *interval != "" {
			d, err := repometa.ParseDuration(*interval)
			if err != nil {
				fmt.Fprintln(sys.stderr, errText(fmt.Errorf("%w: --interval: %w", core.ErrInvalid, err)))
				return ExitUsage
			}
			req.Interval = &d
		}
		inc := repometa.Defaults(req.Kind).Include
		inc.Releases = !*noReleases
		inc.Issues = inc.Issues || *issues
		inc.Wiki = *wiki
		inc.Actions = *actions
		req.Include = &inc
		fmt.Fprintf(sys.stdout, msgAdding, url)
		r, err := lib.Add(ctx, req)
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			exit = ExitError
			if r == nil {
				continue
			}
		}
		fmt.Fprintf(sys.stdout, msgAdded, r.Entry.Key, statusLabel(r.Status()), humanSize(r.Upstream.Stats.SizeBytes))
	}
	return exit
}

func runCreate(args []string, sys system) int {
	fset, data := dataFlagSet("create", sys)
	owner := fset.String("owner", "", "")
	desc := fset.String("description", "", "")
	tags := fset.String("tags", "", "")
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
	r, err := lib.CreateLocal(ctx, *owner, pos[0], *desc, splitList(*tags), "cli")
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgCreated, r.Entry.Key, "/git/"+r.Entry.Key+".git")
	return ExitOK
}

func runFork(args []string, sys system) int {
	fset, data := dataFlagSet("fork", sys)
	owner := fset.String("owner", "", "")
	name := fset.String("name", "", "")
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
	r, err := lib.Fork(ctx, pos[0], *owner, *name, "cli")
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgCreated, r.Entry.Key, "/git/"+r.Entry.Key+".git")
	return ExitOK
}

// ---- sync ----

func runSync(args []string, sys system) int {
	fset, data := dataFlagSet("sync", sys)
	all := fset.Bool("all", false, "")
	due := fset.Bool("due", false, "")
	pos, code, ok := parseArgs(fset, args, 0, -1, sys)
	if !ok {
		return code
	}
	if len(pos) == 0 && !*all && !*due {
		fmt.Fprint(sys.stderr, msgSyncWhat)
		return ExitUsage
	}
	ctx, cancel := cmdContext()
	defer cancel()
	lib, code, ok := openLibrary(ctx, *data, sys)
	if !ok {
		return code
	}
	defer lib.Close()
	refs := pos
	if *all || *due {
		entries, err := lib.List(ctx, store.Query{})
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		for _, e := range entries {
			if *due {
				if next, ok := lib.NextCheck(e); !ok || next.After(lib.Now()) {
					continue
				}
			}
			refs = append(refs, e.ID)
		}
	}
	exit := ExitOK
	for _, ref := range refs {
		rep, err := lib.Sync(ctx, ref, "cli")
		name := rep.Key
		if name == "" {
			name = ref
		}
		if err != nil {
			fmt.Fprintf(sys.stderr, "%s: %s\n", name, errText(err))
			exit = ExitError
			continue
		}
		m := rep.Mirror
		fmt.Fprintf(sys.stdout, msgSynced, name, len(m.New), len(m.Updated), len(m.Rewritten), statusLabel(rep.Status))
		if m.Backup != "" {
			fmt.Fprintf(sys.stdout, msgBackedUp, m.Backup)
		}
		if len(m.Deleted) > 0 {
			fmt.Fprintf(sys.stdout, msgDeletedUpstream, len(m.Deleted))
		}
		for _, n := range rep.Notes {
			fmt.Fprintf(sys.stdout, "  Hinweis: %s\n", n)
		}
		for _, a := range rep.Added {
			fmt.Fprintf(sys.stdout, "  Submodul hinzugefügt: %s\n", a)
		}
	}
	return exit
}

// ---- list / show ----

func runList(args []string, sys system) int {
	fset, data := dataFlagSet("list", sys)
	q := store.Query{}
	fset.StringVar(&q.Tag, "tag", "", "")
	fset.StringVar(&q.Kind, "kind", "", "")
	fset.StringVar(&q.Status, "status", "", "")
	fset.StringVar(&q.Collection, "collection", "", "")
	fset.StringVar(&q.Text, "search", "", "")
	fset.StringVar(&q.Sort, "sort", "", "")
	asJSON := fset.Bool("json", false, "")
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
	entries, err := lib.List(ctx, q)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	if *asJSON {
		return writeJSON(sys, entries)
	}
	if len(entries) == 0 {
		fmt.Fprint(sys.stdout, msgListEmpty)
		return ExitOK
	}
	tw := tabwriter.NewWriter(sys.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tART\tSTATUS\tGRÖSSE\tLETZTER ABGLEICH\tSCHLAGWÖRTER")
	for _, e := range entries {
		last := "–"
		if e.LastSuccess != nil {
			last = e.LastSuccess.Local().Format("02.01.2006 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Key, kindLabel(e.Kind), statusLabel(e.Status), humanSize(e.SizeBytes), last, strings.Join(e.Tags, ", "))
	}
	tw.Flush()
	return ExitOK
}

func runShow(args []string, sys system) int {
	fset, data := dataFlagSet("show", sys)
	asJSON := fset.Bool("json", false, "")
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
	r, err := lib.Get(ctx, pos[0])
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	if *asJSON {
		return writeJSON(sys, map[string]any{"repo": r.R(), "upstream": r.Upstream, "status": r.Status(), "backlinks": r.Backlinks})
	}
	e := r.Entry
	fmt.Fprintf(sys.stdout, "%s\n%s\n\n", e.Key, e.Description)
	fmt.Fprintf(sys.stdout, "Kennung:      %s\nQuelle:       %s\nArt:          %s\nStatus:       %s\nAbgleich:     %s\nGröße:        %s\nBranches:     %d   Versionen: %d   Releases: %d\nLizenz:       %s\nSchlagwörter: %s\nSammlungen:   %s\n",
		e.ID, e.URL, kindLabel(e.Kind), statusLabel(e.Status), e.SyncMode, humanSize(e.SizeBytes),
		e.Branches, e.Versions, e.Releases, orDash(e.License), orDash(strings.Join(e.Tags, ", ")), orDash(strings.Join(e.Collections, ", ")))
	if e.LastError != "" {
		fmt.Fprintf(sys.stdout, "Letzter Fehler: %s\n", e.LastError)
	}
	for _, rel := range r.R().Relations {
		fmt.Fprintf(sys.stdout, "Beziehung:    %s → %s (%s)\n", rel.Type, rel.Target, rel.State)
	}
	for _, bl := range r.Backlinks {
		fmt.Fprintf(sys.stdout, "Rückverweis:  %s ← %s\n", bl.Type, bl.From)
	}
	return ExitOK
}

func writeJSON(sys system, v any) int {
	enc := json.NewEncoder(sys.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	return ExitOK
}

// ---- tags ----

func runTags(args []string, sys system) int {
	fset, data := dataFlagSet("tags", sys)
	pos, code, ok := parseArgs(fset, args, 0, 2, sys)
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
	switch len(pos) {
	case 0:
		tree, err := lib.TagTree(ctx)
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		var walk func(nodes []*core.TagNode, depth int)
		walk = func(nodes []*core.TagNode, depth int) {
			for _, n := range nodes {
				fmt.Fprintf(sys.stdout, "%s%s (%d)\n", strings.Repeat("  ", depth), n.Name, n.Total)
				walk(n.Children, depth+1)
			}
		}
		walk(tree, 0)
	case 2:
		r, err := lib.SetTags(ctx, pos[0], splitList(pos[1]), "cli")
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		fmt.Fprintf(sys.stdout, "%s: %s\n", r.Entry.Key, strings.Join(r.R().Tags, ", "))
	default:
		fmt.Fprint(sys.stderr, msgUsage)
		return ExitUsage
	}
	return ExitOK
}

func runRenameTag(args []string, sys system) int {
	fset, data := dataFlagSet("rename-tag", sys)
	pos, code, ok := parseArgs(fset, args, 2, 2, sys)
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
	n, err := lib.RenameTag(ctx, pos[0], pos[1], "cli")
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgTagRenamed, pos[0], pos[1], n)
	return ExitOK
}

// ---- reindex / doctor ----

func runReindex(args []string, sys system) int {
	fset, data := dataFlagSet("reindex", sys)
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
	res, err := lib.Reindex(ctx)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	fmt.Fprintf(sys.stdout, msgReindexed, res.Repos)
	for _, p := range res.Problems {
		fmt.Fprintf(sys.stderr, "  Problem: %s\n", p)
	}
	if len(res.Problems) > 0 {
		return ExitError
	}
	return ExitOK
}

func runDoctor(args []string, sys system) int {
	fset, data := dataFlagSet("doctor", sys)
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
	rep := lib.Doctor(ctx)
	for _, c := range rep.Checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		fmt.Fprintf(sys.stdout, "%s %s: %s\n", mark, c.Name, c.Detail)
	}
	if !rep.OK() {
		return ExitError
	}
	return ExitOK
}

// ---- cleanup / trash (T-3.3–T-3.5) ----

func runCleanup(args []string, sys system) int {
	fset, data := dataFlagSet("cleanup", sys)
	toTrash := fset.String("to-trash", "", "")
	keep := fset.String("keep", "", "")
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
	switch {
	case *toTrash != "":
		n, err := lib.MoveToTrash(ctx, splitList(*toTrash), "cli")
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		fmt.Fprintf(sys.stdout, msgTrashed, n)
		return ExitOK
	case *keep != "":
		if err := lib.KeepItems(ctx, splitList(*keep), "cli"); err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		fmt.Fprintln(sys.stdout, "Behalten.")
		return ExitOK
	}
	plan, err := lib.CleanupPlan(ctx)
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	if len(plan) == 0 {
		fmt.Fprint(sys.stdout, msgCleanupNone)
		return ExitOK
	}
	tw := tabwriter.NewWriter(sys.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO\tWAS\tGRUND\tGRÖSSE")
	for _, it := range plan {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.ID, it.Key, it.Label, it.Reason, humanSize(it.Size))
	}
	tw.Flush()
	fmt.Fprint(sys.stdout, msgCleanupHint)
	return ExitOK
}

func runTrash(args []string, sys system) int {
	fset, data := dataFlagSet("trash", sys)
	yes := fset.Bool("yes", false, "")
	pos, code, ok := parseArgs(fset, args, 0, -1, sys)
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
	if len(pos) == 0 {
		list, err := lib.Trash()
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		if len(list) == 0 {
			fmt.Fprint(sys.stdout, msgTrashEmpty)
			return ExitOK
		}
		tw := tabwriter.NewWriter(sys.stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tREPO\tWAS\tSEIT\tENDGÜLTIG AB")
		for _, te := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", te.ID, te.Key, te.Label, te.TrashedAt.Local().Format("02.01.2006"), te.Due.Local().Format("02.01.2006"))
		}
		tw.Flush()
		return ExitOK
	}
	switch pos[0] {
	case "restore":
		for _, id := range pos[1:] {
			if err := lib.Restore(ctx, id, "cli"); err != nil {
				fmt.Fprintln(sys.stderr, errText(err))
				return ExitError
			}
			fmt.Fprintf(sys.stdout, "Wiederhergestellt: %s\n", id)
		}
	case "purge":
		if !*yes {
			fmt.Fprint(sys.stderr, msgPurgeConfirm)
			return ExitUsage
		}
		n, err := lib.Purge(ctx, pos[1:], "cli")
		if err != nil {
			fmt.Fprintln(sys.stderr, errText(err))
			return ExitError
		}
		fmt.Fprintf(sys.stdout, "Endgültig gelöscht: %d\n", n)
	default:
		fmt.Fprint(sys.stderr, msgUsage)
		return ExitUsage
	}
	return ExitOK
}

// ---- import (T-9.6) ----

func runImport(args []string, sys system) int {
	fset, data := dataFlagSet("import", sys)
	host := fset.String("host", "github.com", "")
	starred := fset.Bool("starred", false, "")
	kind := fset.String("kind", "", "")
	tags := fset.String("tags", "", "")
	dry := fset.Bool("dry-run", false, "")
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
	k := repometa.Kind(*kind)
	if k == "" {
		k = repometa.KindOwn
		if *starred {
			k = repometa.KindMirror
		}
	}
	res, err := lib.MassImport(ctx, *host, *starred, k, splitList(*tags), *dry, "cli")
	if err != nil {
		fmt.Fprintln(sys.stderr, errText(err))
		return ExitError
	}
	for _, u := range res.Added {
		fmt.Fprintf(sys.stdout, "+ %s\n", u)
	}
	for _, u := range res.Skipped {
		fmt.Fprintf(sys.stdout, "= %s (schon vorhanden)\n", u)
	}
	for _, f := range res.Failed {
		fmt.Fprintf(sys.stderr, "! %s\n", f)
	}
	fmt.Fprintf(sys.stdout, msgImported, len(res.Added), len(res.Skipped), len(res.Failed))
	return ExitOK
}

// ---- helpers ----

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

var _ = time.Second
