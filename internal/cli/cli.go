// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli dispatches the reposhelf command line. It is a thin surface over
// the core packages: it parses arguments, calls into them and maps results to
// output and exit codes. All operating-system access goes through system so
// the whole command line can be tested without a real terminal or profile.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/Fenron-dev/reposhelf/internal/buildinfo"
	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// extraCommands are registered by files that need heavier imports (serve,
// mcp, desktop) so the dispatcher stays readable.
var extraCommands = map[string]func([]string, system) int{}

// Exit codes follow the common shell convention: 2 means wrong usage.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// system is everything the CLI needs from the operating system.
type system struct {
	stdout, stderr io.Writer
	lookupEnv      func(string) (string, bool)
	userConfigDir  func() (string, error)
	executable     func() (string, error)
	// git overrides the git executable (tests); nil = search.
	git *gitcli.Git
	// stdin feeds "host-token set"; keychain replaces the OS keychain (tests).
	stdin    io.Reader
	keychain sources.Keychain
}

// Run executes the command given in args (without the program name) and
// returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, system{
		stdout:        stdout,
		stderr:        stderr,
		lookupEnv:     os.LookupEnv,
		userConfigDir: os.UserConfigDir,
		executable:    os.Executable,
		stdin:         os.Stdin,
	})
}

func run(args []string, sys system) int {
	if len(args) == 0 {
		fmt.Fprint(sys.stdout, msgUsage)
		return ExitOK
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(sys.stdout, msgUsage)
		return ExitOK
	case "version", "--version":
		fmt.Fprintf(sys.stdout, msgVersion, buildinfo.String())
		return ExitOK
	case "config":
		return runConfig(args[1:], sys)
	case "init":
		return runInit(args[1:], sys)
	case "add":
		return runAdd(args[1:], sys)
	case "create":
		return runCreate(args[1:], sys)
	case "fork":
		return runFork(args[1:], sys)
	case "sync":
		return runSync(args[1:], sys)
	case "list", "ls":
		return runList(args[1:], sys)
	case "show":
		return runShow(args[1:], sys)
	case "tags":
		return runTags(args[1:], sys)
	case "rename-tag":
		return runRenameTag(args[1:], sys)
	case "reindex":
		return runReindex(args[1:], sys)
	case "doctor":
		return runDoctor(args[1:], sys)
	case "cleanup":
		return runCleanup(args[1:], sys)
	case "trash":
		return runTrash(args[1:], sys)
	case "import":
		return runImport(args[1:], sys)
	case "host-token":
		return runHostToken(args[1:], sys)
	default:
		if h, ok := extraCommands[args[0]]; ok {
			return h(args[1:], sys)
		}
		fmt.Fprintf(sys.stderr, msgUnknownCommand, args[0])
		fmt.Fprint(sys.stderr, msgUsage)
		return ExitUsage
	}
}

// runConfig shows which data directory and settings are in effect, so the
// user can see why RepoShelf uses a folder before any data is written there.
func runConfig(args []string, sys system) int {
	fset := newFlagSet("config", sys)
	dataFlag := fset.String("data", "", "")
	if code, ok := parseFlags(fset, args, sys); !ok {
		return code
	}

	loc, code, ok := resolveDataDir(*dataFlag, sys)
	if !ok {
		return code
	}

	loaded, err := config.Load(loc.Path, sys.lookupEnv)
	if err != nil {
		fmt.Fprintf(sys.stderr, msgConfigInvalid, err)
		return ExitError
	}
	for _, w := range loaded.Warnings {
		fmt.Fprintf(sys.stderr, msgConfigWarning, config.FileName, w)
	}

	fileNote := ""
	if !loaded.FileExists {
		fileNote = msgConfigFileMissing
	}
	fmt.Fprintf(sys.stdout, msgConfigReport,
		loc.Path, sourceLabels[loc.Source], loaded.Path, fileNote, loaded.Config.Server.Listen)
	return ExitOK
}

// resolveDataDir runs the data directory chain and reports failures to the
// user. ok is false when the command must stop with the returned exit code.
func resolveDataDir(flagValue string, sys system) (datadir.Location, int, bool) {
	loc, err := datadir.Resolve(datadir.Env{
		Flag:          flagValue,
		LookupEnv:     sys.lookupEnv,
		UserConfigDir: sys.userConfigDir,
		Executable:    sys.executable,
	})
	if err == nil {
		return loc, ExitOK, true
	}

	var ue *datadir.UnreachableError
	switch {
	case errors.Is(err, datadir.ErrNotConfigured):
		fmt.Fprint(sys.stderr, msgNotConfigured)
	case errors.As(err, &ue):
		fmt.Fprintf(sys.stderr, msgUnreachable, ue.Path, sourceLabels[ue.Source], unreachableReason(ue.Err))
	default:
		fmt.Fprintln(sys.stderr, err)
	}
	return datadir.Location{}, ExitError, false
}

func unreachableReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return reasonNotExist
	case errors.Is(err, datadir.ErrNotDir):
		return reasonNotDir
	case errors.Is(err, datadir.ErrPointerEmpty):
		return reasonPointerEmpty
	case errors.Is(err, datadir.ErrPointerRelative):
		return reasonPointerRelative
	case errors.Is(err, fs.ErrPermission):
		return reasonPermission
	default:
		return err.Error()
	}
}

func newFlagSet(name string, sys system) *flag.FlagSet {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(sys.stderr)
	fset.Usage = func() { fmt.Fprint(sys.stderr, msgUsage) }
	return fset
}

// parseFlags parses args and rejects positional arguments. ok is false when
// the command must stop with the returned exit code.
func parseFlags(fset *flag.FlagSet, args []string, sys system) (int, bool) {
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK, false
		}
		return ExitUsage, false
	}
	if fset.NArg() > 0 {
		fmt.Fprintf(sys.stderr, msgExtraArgs, fset.Arg(0))
		fmt.Fprint(sys.stderr, msgUsage)
		return ExitUsage, false
	}
	return ExitOK, true
}
