// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Limits for ZIP imports: archives come from outside.
const (
	maxZipBytes = 4 << 30
	maxZipFiles = 200000
)

// ImportZIP creates an own project from a ZIP archive (T-9.2). If the
// archive contains a git repository (.git folder), its whole history is
// imported; otherwise the files become the first commit. A single
// top-level folder (as in GitHub's "Download ZIP") is removed.
func (l *Library) ImportZIP(ctx context.Context, zr *zip.Reader, owner, name, description string, tags []string, actor string) (*Repo, error) {
	tmp, err := os.MkdirTemp(l.Layout.Path(".reposhelf"), "zip-import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp) // our own temporary folder
	if err := unzip(zr, tmp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	root := tmp
	if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 && entries[0].IsDir() && entries[0].Name() != ".git" {
		root = filepath.Join(tmp, entries[0].Name())
		if name == "" {
			name = entries[0].Name()
		}
	}
	if name == "" {
		return nil, fmt.Errorf("%w: name missing", ErrInvalid)
	}
	repo, err := l.CreateLocal(ctx, owner, name, description, tags, actor)
	if err != nil {
		return nil, err
	}
	unlock := l.locks.lock(repo.R().ID)
	defer unlock()
	g := l.Git
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		// A zipped clone: take over every branch and tag.
		if _, err := g.Run(ctx, repo.Paths.Mirror, nil, "fetch", "--quiet", "--end-of-options", filepath.Join(root, ".git"),
			"refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*"); err != nil {
			return nil, err
		}
	} else {
		base := []string{"-c", "user.name=RepoShelf", "-c", "user.email=reposhelf@localhost",
			"--git-dir", repo.Paths.Mirror, "--work-tree", root}
		if _, err := g.Run(ctx, root, nil, append(base, "add", "--all", "--force", ".")...); err != nil {
			return nil, err
		}
		if _, err := g.Run(ctx, root, nil, append(base, "commit", "--quiet", "--allow-empty", "-m", "Import aus ZIP")...); err != nil {
			return nil, err
		}
		_ = os.Remove(filepath.Join(repo.Paths.Mirror, "index")) // bare repositories need no index
	}
	l.Git.FixHead(ctx, repo.Paths.Mirror)
	l.refreshStats(ctx, repo)
	l.Record(Activity{Actor: actorOr(actor), Action: "import_zip", RepoID: repo.R().ID})
	return l.Get(ctx, repo.R().ID)
}

func unzip(zr *zip.Reader, dir string) error {
	var total int64
	if len(zr.File) > maxZipFiles {
		return fmt.Errorf("too many files (%d)", len(zr.File))
	}
	for _, f := range zr.File {
		name := filepath.FromSlash(strings.TrimPrefix(f.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path %q", f.Name)
		}
		p := filepath.Join(dir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue // no symlinks or devices from foreign archives
		}
		total += int64(f.UncompressedSize64)
		if total > maxZipBytes {
			return errors.New("archive too large")
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm()|0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
