// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ---- Models for programs: a Hugging Face cache (T-9.11) ----
//
// Programs that load models with huggingface_hub (transformers, diffusers,
// TTS tools …) look in $HF_HOME/hub/models--org--name. RepoShelf keeps
// such a folder for every model it mirrors, so a tool can run offline:
//
//	HF_HOME=<data>/runtime/huggingface  HF_HUB_OFFLINE=1
//
// The files are hard links to the LFS objects of the mirror (made
// read-only first, so a program cannot change the archive), small files
// are written out: nothing large is stored twice. The folder is derived
// data and can be rebuilt at any time.

// RuntimeDir holds data prepared for programs (below the data folder).
const RuntimeDir = "runtime"

// maxPlainFile bounds files kept in git (not LFS) that are written out.
const maxPlainFile = 512 << 20

// HFHome is the HF_HOME folder RepoShelf keeps.
func (l *Library) HFHome() string {
	return filepath.Join(l.Layout.Root, RuntimeDir, "huggingface")
}

// HFModelDir is the cache folder of a mirrored model, "" for others.
func (l *Library) HFModelDir(r *Repo) string {
	src := r.R().Source
	if src.Host != sources.HuggingFaceHost || strings.Contains(src.Owner, "/") {
		return ""
	}
	return filepath.Join(l.HFHome(), "hub", "models--"+src.Owner+"--"+src.Name)
}

// exportHF writes the cache folder for the current revision of a model.
// It returns how many LFS files are not (yet) in the mirror.
func (l *Library) exportHF(ctx context.Context, r *Repo) (int, error) {
	dir := l.HFModelDir(r)
	if dir == "" || !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		return 0, nil
	}
	out, err := l.Git.Run(ctx, r.Paths.Mirror, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return 0, err
	}
	commit := strings.TrimSpace(string(out))
	if !safeSegment(commit) {
		return 0, fmt.Errorf("unexpected commit %q", commit)
	}
	snaps := filepath.Join(dir, "snapshots")
	final := filepath.Join(snaps, commit)
	files, err := l.Git.TreeFiles(ctx, r.Paths.Mirror, commit)
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(final); err == nil {
		return 0, l.writeHFRef(dir, commit)
	}
	tmp := final + ".tmp"
	_ = os.RemoveAll(tmp) // a leftover of an interrupted export (ours)
	missing := 0
	for _, f := range files {
		if f.Mode != "100644" && f.Mode != "100755" {
			continue // symlinks and submodules are not model files
		}
		rel := path.Clean(f.Path)
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			continue
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
			return 0, err
		}
		if f.Size <= 1024 {
			b, err := l.Git.ReadBlob(ctx, r.Paths.Mirror, f.Blob, 1024)
			if err != nil {
				return 0, err
			}
			if oid, _, ok := gitcli.ParseLFSPointer(b); ok {
				obj := gitcli.LFSObjectPath(r.Paths.Mirror, oid)
				if _, err := os.Stat(obj); err != nil {
					missing++
					continue
				}
				// Read-only first: a program writing into the file would
				// otherwise change the archived object behind the link.
				if err := os.Chmod(obj, 0o444); err != nil {
					return 0, err
				}
				if err := os.Link(obj, dst); err != nil {
					return 0, fmt.Errorf("%s: %w", rel, err)
				}
				continue
			}
			if err := os.WriteFile(dst, b, 0o444); err != nil {
				return 0, err
			}
			continue
		}
		b, err := l.Git.ReadBlob(ctx, r.Paths.Mirror, f.Blob, maxPlainFile)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", rel, err)
		}
		if err := os.WriteFile(dst, b, 0o444); err != nil {
			return 0, err
		}
	}
	// Older snapshots are ours and derived: only the current one stays.
	if entries, err := os.ReadDir(snaps); err == nil {
		for _, e := range entries {
			if e.Name() != commit+".tmp" {
				if err := removeTree(filepath.Join(snaps, e.Name())); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		return 0, err
	}
	return missing, l.writeHFRef(dir, commit)
}

func (l *Library) writeHFRef(dir, commit string) error {
	if err := os.MkdirAll(filepath.Join(dir, "refs"), dirPerm); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, "refs", "main"), []byte(commit), filePerm)
}

// removeTree deletes a folder RepoShelf wrote, including read-only files
// (Windows refuses to delete those).
func removeTree(p string) error {
	_ = filepath.WalkDir(p, func(q string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(q, 0o755)
		} else if d.Type().IsRegular() {
			_ = os.Chmod(q, 0o644)
		}
		return nil
	})
	err := os.RemoveAll(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// HFUsage describes how a program finds a model kept here.
type HFUsage struct {
	Home    string // HF_HOME
	Dir     string // the model's folder
	Ready   bool   // a snapshot exists
	ModelID string
}

// HFUsageOf returns how to use a mirrored model, ok false for other repos.
func (l *Library) HFUsageOf(r *Repo) (HFUsage, bool) {
	dir := l.HFModelDir(r)
	if dir == "" {
		return HFUsage{}, false
	}
	_, err := os.Stat(filepath.Join(dir, "refs", "main"))
	return HFUsage{Home: l.HFHome(), Dir: dir, Ready: err == nil, ModelID: r.R().Source.Owner + "/" + r.R().Source.Name}, true
}
