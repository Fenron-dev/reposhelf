// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Repo is everything known about one repository.
type Repo struct {
	Doc      *repometa.Document
	Entry    store.Entry
	Upstream Upstream
	Paths    Paths
	// Backlinks are relations of other repositories pointing here.
	Backlinks []store.Edge
}

// Paths of a repository inside the data folder.
type Paths struct {
	Meta, RepoFile, Mirror, Releases string
}

// R is a shortcut to the repo.yaml content.
func (r *Repo) R() *repometa.Repo { return &r.Doc.Repo }

// Status returns the effective status (override > detected).
func (r *Repo) Status() string { return r.Entry.Status }

func (l *Library) paths(src repometa.Source) (Paths, error) {
	var p Paths
	var err error
	if p.Meta, err = l.Layout.MetaDir(src); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	p.RepoFile = filepath.Join(p.Meta, repometa.FileName)
	if p.Mirror, err = l.Layout.MirrorDir(src); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if p.Releases, err = l.Layout.ReleasesDir(src); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return p, nil
}

// List returns index entries matching q.
func (l *Library) List(ctx context.Context, q store.Query) ([]store.Entry, error) {
	return l.Store.List(ctx, q)
}

// Get loads a repository by ID or host/owner/name. It reads the files, not
// the index, so hand edits are visible immediately.
func (l *Library) Get(ctx context.Context, ref string) (*Repo, error) {
	ref = strings.TrimSuffix(strings.Trim(ref, "/ "), ".git")
	e, err := l.Store.Get(ctx, ref)
	if errors.Is(err, store.ErrNotFound) {
		e, err = l.byShortRef(ctx, ref)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%s: %w", ref, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return l.load(ctx, e.MetaPath)
}

// load reads a repository from its meta folder.
func (l *Library) load(ctx context.Context, metaDir string) (*Repo, error) {
	doc, err := repometa.Load(filepath.Join(metaDir, repometa.FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", metaDir, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	p, err := l.paths(doc.Repo.Source)
	if err != nil {
		return nil, err
	}
	up, err := readUpstream(metaDir)
	if err != nil {
		l.Log.Warn("upstream.json unreadable", "dir", metaDir, "err", err)
	}
	r := &Repo{Doc: doc, Upstream: up, Paths: p}
	r.Entry = l.entry(ctx, r, false)
	if bl, err := l.Store.Backlinks(ctx, r.Entry); err == nil {
		r.Backlinks = bl
	}
	return r, nil
}

// Update changes repo.yaml of ref under the repository lock and refreshes
// the index. fn may return an error to abort without writing.
func (l *Library) Update(ctx context.Context, ref string, fn func(r *repometa.Repo) error) (*Repo, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	// Re-read under the lock: another writer may have changed the file.
	r, err = l.load(ctx, r.Paths.Meta)
	if err != nil {
		return nil, err
	}
	if err := fn(r.R()); err != nil {
		return nil, err
	}
	if err := l.save(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// save writes repo.yaml atomically and refreshes the index entry.
func (l *Library) save(ctx context.Context, r *Repo) error {
	b, err := r.Doc.Marshal()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := os.MkdirAll(r.Paths.Meta, dirPerm); err != nil {
		return err
	}
	if err := atomicfile.WriteFile(r.Paths.RepoFile, b, filePerm); err != nil {
		return err
	}
	r.Entry = l.entry(ctx, r, true)
	if err := l.Store.Upsert(ctx, r.Entry); err != nil {
		return err
	}
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
	return nil
}

const dirPerm = 0o755

// readmeLimit bounds README text in the index.
const readmeLimit = 512 << 10

// entry builds the index row of r. withReadme reads the README from git
// (costly; only after writes and during reindex).
func (l *Library) entry(ctx context.Context, r *Repo, withReadme bool) store.Entry {
	rp := r.R()
	up := r.Upstream
	e := store.Entry{
		ID: rp.ID, Key: rp.Source.Key(), Host: rp.Source.Host, Owner: rp.Source.Owner, Name: rp.Source.Name,
		URL: rp.Source.URL, Kind: string(rp.Kind), SyncMode: string(rp.Sync.Mode),
		LastSuccess: rp.Sync.LastSuccess, LastCheck: rp.Sync.LastCheck,
		DetectedStatus: up.Status, FailingSince: up.FailingSince,
		Buildable: rp.Buildable.Enabled, Created: rp.Created, MetaPath: r.Paths.Meta,
		Tags: rp.Tags, Collections: rp.Collections,
		SizeBytes: up.Stats.SizeBytes, Releases: up.Stats.Releases, Versions: up.Stats.Versions, Branches: up.Stats.Branches,
	}
	if rp.Sync.Interval != nil {
		e.Interval = time.Duration(*rp.Sync.Interval)
	}
	if rp.Sync.LastError != nil {
		e.LastError = *rp.Sync.LastError
	}
	if rp.StatusOverride != nil {
		e.StatusOverride = string(*rp.StatusOverride)
	}
	e.Status = effectiveStatus(rp, up)
	if rp.License.SPDX != nil {
		e.License = *rp.License.SPDX
	}
	if m := up.Metadata; m != nil {
		e.Description, e.Language, e.Topics, e.Stars = m.Description, m.Language, m.Topics, m.Stars
		e.DefaultBranch, e.UpstreamArchived, e.PushedAt = m.DefaultBranch, m.Archived, m.PushedAt
		if e.License == "" {
			e.License = m.License
		}
	}
	if e.Description == "" {
		if d, ok := unknownString(r.Doc, "description"); ok {
			e.Description = d
		}
	}
	for _, rel := range rp.Relations {
		e.Relations = append(e.Relations, store.Edge{From: rp.ID, To: rel.Target, Type: rel.Type, State: string(rel.State), Source: rel.Source})
	}
	if rp.ForkOf != nil {
		e.Relations = append(e.Relations, store.Edge{From: rp.ID, To: *rp.ForkOf, Type: "fork_of", State: string(repometa.RelationConfirmed), Source: "fork"})
	}
	if b, err := os.ReadFile(filepath.Join(r.Paths.Meta, layout.NotesFile)); err == nil {
		e.Notes = string(b)
	}
	if withReadme && l.Git.HasCommits(ctx, r.Paths.Mirror) {
		if _, b, err := l.Git.Readme(ctx, r.Paths.Mirror, "HEAD", readmeLimit); err == nil {
			e.Readme = string(b)
		}
	}
	return e
}

// unknownString is a hook for descriptions of local repositories: they may
// carry a "description" key by hand (kept as unknown key).
func unknownString(doc *repometa.Document, key string) (string, bool) {
	return doc.Extra(key)
}

func effectiveStatus(r *repometa.Repo, up Upstream) string {
	if r.StatusOverride != nil {
		return string(*r.StatusOverride)
	}
	if r.Source.Host == repometa.LocalHost {
		return StatusLocal
	}
	if up.Status == "" {
		return StatusNew
	}
	return up.Status
}

// ReindexResult reports a rebuild.
type ReindexResult struct {
	Repos    int
	Problems []string
}

// Reindex rebuilds the SQLite index from the files (D-004). Files that
// cannot be read are reported, not skipped silently.
func (l *Library) Reindex(ctx context.Context) (ReindexResult, error) {
	var res ReindexResult
	files, err := l.Layout.RepoFiles()
	if err != nil {
		return res, err
	}
	var entries []store.Entry
	seen := map[string]string{}
	for _, f := range files {
		r, err := l.loadForIndex(ctx, filepath.Dir(f))
		if err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		fold := store.FoldKey(r.Entry.Key)
		if other, dup := seen[fold]; dup {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: same folder as %s", f, other))
			continue
		}
		seen[fold] = f
		entries = append(entries, r.Entry)
	}
	if err := l.Store.Replace(ctx, entries); err != nil {
		return res, err
	}
	res.Repos = len(entries)
	l.events.publish(Event{Kind: "reindex"})
	return res, nil
}

func (l *Library) loadForIndex(ctx context.Context, metaDir string) (*Repo, error) {
	doc, err := repometa.Load(filepath.Join(metaDir, repometa.FileName))
	if err != nil {
		return nil, err
	}
	p, err := l.paths(doc.Repo.Source)
	if err != nil {
		return nil, err
	}
	if p.Meta != metaDir {
		return nil, fmt.Errorf("repo.yaml describes %s but lies in another folder", doc.Repo.Source.Key())
	}
	up, _ := readUpstream(metaDir)
	r := &Repo{Doc: doc, Upstream: up, Paths: p}
	r.Entry = l.entry(ctx, r, true)
	return r, nil
}

// byShortRef accepts "owner/name" (or just "name") when it is unique.
func (l *Library) byShortRef(ctx context.Context, ref string) (store.Entry, error) {
	all, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return store.Entry{}, err
	}
	var hits []store.Entry
	want := store.FoldKey(ref)
	for _, e := range all {
		if store.FoldKey(e.Owner+"/"+e.Name) == want || store.FoldKey(e.Name) == want {
			hits = append(hits, e)
		}
	}
	if len(hits) != 1 {
		return store.Entry{}, store.ErrNotFound
	}
	return hits[0], nil
}
