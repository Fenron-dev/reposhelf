// SPDX-License-Identifier: GPL-3.0-or-later

// Package layout knows where everything lives inside a data folder
// (KONZEPT 5.1). Every path that contains a name from outside is built with
// safepath, so no other package needs to join network names itself.
//
// It depends on safepath, atomicfile and repometa (for Source).
package layout

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

// Folder names below the data folder root.
const (
	DirRepos       = "repos"
	DirMeta        = "meta"
	DirReleases    = "releases"
	DirCapsules    = "capsules"
	DirBuilds      = "builds"
	DirCollections = "collections"
	DirNotes       = "notes"
	DirTrash       = "trash"
	DirInternal    = ".reposhelf"
	DirLocks       = "locks"
	DirLogs        = "logs"

	// MirrorSuffix marks bare repositories (KONZEPT 5.1: Fero.git).
	MirrorSuffix = ".git"
	// NotesFile and UpstreamFile sit next to repo.yaml.
	NotesFile    = "notes.md"
	UpstreamFile = "upstream.json"
	// IndexFile is the SQLite index; it can always be rebuilt.
	IndexFile = "index.db"
	// ConfigFile is the instance configuration.
	ConfigFile = "reposhelf.yaml"

	dirPerm = 0o755
	// staleTempAge protects temporary files of a write that is still running
	// in another RepoShelf process (server and CLI share a data folder).
	staleTempAge = time.Hour
)

// Layout resolves paths inside one data folder.
type Layout struct {
	Root string
}

// skeleton is created by Init; order matters only for readability.
var skeleton = []string{
	DirRepos, DirMeta, DirReleases, DirCapsules, DirBuilds, DirCollections,
	DirNotes, DirTrash, DirInternal,
	filepath.Join(DirInternal, DirLocks), filepath.Join(DirInternal, DirLogs),
}

// Init creates the folder structure below root. root itself must already
// exist: creating the data folder is a decision of the user (cli init).
// Existing folders and files are left untouched.
func Init(root string) (Layout, error) {
	info, err := os.Stat(root)
	if err != nil {
		return Layout{}, fmt.Errorf("data folder %s: %w", root, err)
	}
	if !info.IsDir() {
		return Layout{}, fmt.Errorf("data folder %s: not a directory", root)
	}
	for _, d := range skeleton {
		if err := os.MkdirAll(filepath.Join(root, d), dirPerm); err != nil {
			return Layout{}, fmt.Errorf("create %s: %w", d, err)
		}
	}
	return Layout{Root: root}, nil
}

// Path returns a path below the root built from fixed folder names.
func (l Layout) Path(parts ...string) string {
	return filepath.Join(append([]string{l.Root}, parts...)...)
}

// IndexPath is the SQLite index.
func (l Layout) IndexPath() string { return l.Path(DirInternal, IndexFile) }

// LocksDir holds lock files.
func (l Layout) LocksDir() string { return l.Path(DirInternal, DirLocks) }

// LogsDir holds log files.
func (l Layout) LogsDir() string { return l.Path(DirInternal, DirLogs) }

// TrashDir is the trash.
func (l Layout) TrashDir() string { return l.Path(DirTrash) }

// MetaDir is meta/<host>/<owner…>/<name>.
func (l Layout) MetaDir(src repometa.Source) (string, error) {
	return safepath.Join(l.Path(DirMeta), src.Segments()...)
}

// RepoFile is the repo.yaml of a repository.
func (l Layout) RepoFile(src repometa.Source) (string, error) {
	dir, err := l.MetaDir(src)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, repometa.FileName), nil
}

// MirrorDir is repos/<host>/<owner…>/<name>.git.
func (l Layout) MirrorDir(src repometa.Source) (string, error) {
	segs := src.Segments()
	segs[len(segs)-1] += MirrorSuffix
	return safepath.Join(l.Path(DirRepos), segs...)
}

// ReleasesDir is releases/<host>/<owner…>/<name>.
func (l Layout) ReleasesDir(src repometa.Source) (string, error) {
	return safepath.Join(l.Path(DirReleases), src.Segments()...)
}

// RepoFiles returns the paths of all repo.yaml files below meta/, sorted.
func (l Layout) RepoFiles() ([]string, error) {
	var out []string
	root := l.Path(DirMeta)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if !d.IsDir() && d.Name() == repometa.FileName {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// CleanTemp removes temporary files that an interrupted atomic write left
// behind (T-0.13). Only regular files with atomicfile's prefix that are older
// than an hour are removed – RepoShelf wrote them, and nothing else may use
// that prefix (safepath). Bare repositories are skipped: git manages them.
func (l Layout) CleanTemp(now time.Time) ([]string, error) {
	var removed []string
	err := filepath.WalkDir(l.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != l.Root && strings.HasSuffix(d.Name(), MirrorSuffix) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !atomicfile.IsTemp(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < staleTempAge {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		removed = append(removed, p)
		return nil
	})
	return removed, err
}
