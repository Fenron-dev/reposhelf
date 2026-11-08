// SPDX-License-Identifier: GPL-3.0-or-later

// Package datadir finds the RepoShelf data directory (KONZEPT 4.4).
//
// The lookup order is: --data flag, REPOSHELF_DATA environment variable
// (Docker sets /data), the pointer file the user chose in the profile, and
// finally a portable RepoShelf-Daten/ folder next to the program.
//
// The hard rule (AGENTS.md invariant 5): once a step names a directory, that
// directory must be reachable. If it is not, Resolve reports an error and
// never falls back to a later step, so data is never written to a place the
// user did not choose. Only the portable folder is discovered by existence.
//
// The package only depends on the standard library. All operating-system
// access goes through Env so the whole chain is testable with temp folders.
package datadir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvVar names the environment variable that sets the data directory.
	EnvVar = "REPOSHELF_DATA"
	// PortableDirName is the folder next to the program that enables
	// portable mode (e.g. on a USB stick).
	PortableDirName = "RepoShelf-Daten"

	pointerAppDir   = "RepoShelf"
	pointerFileName = "datadir.txt"
)

// Source tells which step of the chain supplied the data directory.
type Source int

// Steps of the chain, in lookup order.
const (
	SourceFlag Source = iota + 1
	SourceEnv
	SourcePointer
	SourcePortable
)

func (s Source) String() string {
	switch s {
	case SourceFlag:
		return "flag"
	case SourceEnv:
		return "env"
	case SourcePointer:
		return "pointer"
	case SourcePortable:
		return "portable"
	default:
		return fmt.Sprintf("Source(%d)", int(s))
	}
}

// ErrNotConfigured means no step of the chain named a data directory; the
// caller should start the setup instead of guessing a location.
var ErrNotConfigured = errors.New("no data directory configured")

// Errors wrapped by UnreachableError.
var (
	ErrNotDir          = errors.New("not a directory")
	ErrPointerEmpty    = errors.New("pointer file is empty")
	ErrPointerRelative = errors.New("pointer file must contain an absolute path")
)

// UnreachableError means a step named a data directory that cannot be used.
type UnreachableError struct {
	Path   string
	Source Source
	Err    error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("data directory %q (from %s) is unreachable: %v", e.Path, e.Source, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// Location is a resolved, reachable data directory.
type Location struct {
	Path   string
	Source Source
}

// Env bundles the inputs of the chain.
type Env struct {
	// Flag is the value of --data; empty means not given.
	Flag          string
	LookupEnv     func(key string) (string, bool)
	UserConfigDir func() (string, error)
	Executable    func() (string, error)
}

// OSEnv returns an Env backed by the real operating system.
func OSEnv(flag string) Env {
	return Env{
		Flag:          flag,
		LookupEnv:     os.LookupEnv,
		UserConfigDir: os.UserConfigDir,
		Executable:    os.Executable,
	}
}

// PointerPath returns where the pointer file lives in the user profile.
func PointerPath(userConfigDir string) string {
	return filepath.Join(userConfigDir, pointerAppDir, pointerFileName)
}

// Resolve walks the chain and returns the first data directory it finds.
func Resolve(env Env) (Location, error) {
	if env.Flag != "" {
		p, err := filepath.Abs(env.Flag)
		if err != nil {
			return Location{}, &UnreachableError{Path: env.Flag, Source: SourceFlag, Err: err}
		}
		return requireDir(p, SourceFlag)
	}

	// An empty variable is treated as unset: it is the usual way to "unset"
	// a variable in compose files and shells.
	if v, ok := env.LookupEnv(EnvVar); ok && v != "" {
		p, err := filepath.Abs(v)
		if err != nil {
			return Location{}, &UnreachableError{Path: v, Source: SourceEnv, Err: err}
		}
		return requireDir(p, SourceEnv)
	}

	if loc, found, err := fromPointer(env); found || err != nil {
		return loc, err
	}

	if loc, found, err := fromPortable(env); found || err != nil {
		return loc, err
	}

	return Location{}, ErrNotConfigured
}

// fromPointer reads the pointer file. A missing profile directory or pointer
// file means the user never chose a folder; everything else is an error.
func fromPointer(env Env) (Location, bool, error) {
	cfgDir, err := env.UserConfigDir()
	if err != nil {
		// No profile (e.g. a container without $HOME): this step cannot apply.
		return Location{}, false, nil
	}
	pointer := PointerPath(cfgDir)
	raw, err := os.ReadFile(pointer)
	if errors.Is(err, fs.ErrNotExist) {
		return Location{}, false, nil
	}
	if err != nil {
		return Location{}, true, &UnreachableError{Path: pointer, Source: SourcePointer, Err: fmt.Errorf("read pointer file: %w", err)}
	}

	p := strings.TrimSpace(string(raw))
	switch {
	case p == "":
		return Location{}, true, &UnreachableError{Path: pointer, Source: SourcePointer, Err: ErrPointerEmpty}
	case !filepath.IsAbs(p):
		return Location{}, true, &UnreachableError{Path: p, Source: SourcePointer, Err: ErrPointerRelative}
	}
	loc, err := requireDir(filepath.Clean(p), SourcePointer)
	return loc, true, err
}

// fromPortable looks for RepoShelf-Daten/ next to the program.
func fromPortable(env Env) (Location, bool, error) {
	exe, err := env.Executable()
	if err != nil {
		return Location{}, false, nil
	}
	// Follow symlinks: a link in /usr/local/bin must not hide the stick the
	// real binary lives on.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	candidate := filepath.Join(PortableBase(exe), PortableDirName)

	info, err := os.Stat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return Location{}, false, nil
	}
	if err != nil {
		return Location{}, true, &UnreachableError{Path: candidate, Source: SourcePortable, Err: err}
	}
	if !info.IsDir() {
		return Location{}, true, &UnreachableError{Path: candidate, Source: SourcePortable, Err: ErrNotDir}
	}
	return Location{Path: candidate, Source: SourcePortable}, true, nil
}

// PortableBase returns the folder that holds the portable data folder. For a
// macOS app bundle (X.app/Contents/MacOS/reposhelf) that is the folder
// containing X.app, not the folder inside the bundle (KONZEPT 9, VaultApp
// pitfall): files inside a bundle break its signature and vanish on update.
func PortableBase(exePath string) string {
	dir := filepath.Dir(exePath)
	contents := filepath.Dir(dir)
	bundle := filepath.Dir(contents)
	if filepath.Base(dir) == "MacOS" &&
		filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(strings.ToLower(filepath.Base(bundle)), ".app") {
		return filepath.Dir(bundle)
	}
	return dir
}

func requireDir(path string, src Source) (Location, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Location{}, &UnreachableError{Path: path, Source: src, Err: err}
	}
	if !info.IsDir() {
		return Location{}, &UnreachableError{Path: path, Source: src, Err: ErrNotDir}
	}
	return Location{Path: path, Source: src}, nil
}
