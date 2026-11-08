// SPDX-License-Identifier: GPL-3.0-or-later

// Package atomicfile writes state files so that a reader, or RepoShelf after
// a crash or power loss, sees either the complete old content or the complete
// new content, never a partial file (AGENTS.md invariant 3, D-004).
//
// The data goes to a temporary file in the target's own directory, is flushed
// to disk and then renamed over the target. A rename within one directory is
// atomic on the file systems RepoShelf supports; a temporary file elsewhere
// could sit on another volume, where rename degrades to copy. The directory is
// flushed afterwards so the rename itself survives a power loss.
//
// The package depends only on the standard library.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TempPrefix starts the name of every temporary file this package creates.
// It is fixed, not derived from the target name, so a long target name cannot
// push the temporary name over the file name limit, and leftovers from a
// crash can be recognised (IsTemp). Names from the network may not use it
// (safepath), so a leftover is always RepoShelf's own file.
const TempPrefix = ".reposhelf-tmp-"

// ErrNoFileName is returned for a path that does not name a file.
var ErrNoFileName = errors.New("path does not name a file")

// IsTemp reports whether name, a base name, is a temporary file left by this
// package.
func IsTemp(name string) bool { return strings.HasPrefix(name, TempPrefix) }

// WriteFile atomically replaces the file at path with data. See Write.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	return Write(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write atomically replaces the file at path with whatever fill writes.
//
// The parent directory must exist; Write never creates directories, so a
// mistyped or unmounted location fails instead of being created silently
// (AGENTS.md invariant 5). If fill or any step fails, the target is left as it
// was and the temporary file is removed. The new file gets mode perm (only the
// read-only bit on Windows).
//
// If path is a symbolic link, the file it points to is replaced and the link
// stays, so a user who linked a file to another place keeps that setup.
func Write(path string, perm fs.FileMode, fill func(io.Writer) error) (err error) {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(target)

	f, err := os.CreateTemp(dir, TempPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if err == nil {
			return
		}
		if !closed {
			_ = f.Close()
		}
		_ = os.Remove(tmp)
	}()

	if err = fill(f); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Chmod(perm); err != nil {
		return fmt.Errorf("set mode of %s: %w", path, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("flush %s: %w", path, err)
	}
	closed = true
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err = os.Rename(tmp, target); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	// The new content is in place from here on; a failed directory flush must
	// not remove it, so it is reported without the cleanup above.
	if syncErr := syncDir(dir); syncErr != nil {
		return fmt.Errorf("flush directory of %s: %w", path, syncErr)
	}
	return nil
}

// resolveTarget returns the file that is to be replaced: path itself, or the
// final target if path is a symbolic link.
func resolveTarget(path string) (string, error) {
	base := filepath.Base(path)
	if path == "" || base == "." || base == ".." || base == string(filepath.Separator) ||
		strings.HasSuffix(path, "/") || strings.HasSuffix(path, string(filepath.Separator)) {
		return "", fmt.Errorf("%q: %w", path, ErrNoFileName)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		// A missing file is fine (first write); other problems show up when
		// the temporary file is created or renamed.
		return path, nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve link %s: %w", path, err)
	}
	return target, nil
}
