// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// Functions used by instance sync (Phase 8). The peer package decides what
// to take over; the core only writes files the usual way (atomic, locked).

// AdoptRepo creates a repository received from a paired instance. The ID
// stays the same on both instances; local sync state starts empty.
func (l *Library) AdoptRepo(ctx context.Context, r repometa.Repo, up Upstream, notes, actor string) (*Repo, error) {
	r.Sync.LastCheck, r.Sync.LastSuccess, r.Sync.LastError = nil, nil, nil
	if _, err := l.Store.ByFolder(ctx, r.Source.Key()); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, r.Source.Key())
	}
	repo, err := l.create(ctx, r, sources.Parsed{Source: r.Source, Provider: up.Provider, WebURL: up.WebURL}, up.Metadata, notes)
	if err != nil {
		return nil, err
	}
	up.FailingSince, up.HostDown = nil, false
	if err := writeUpstream(repo.Paths.Meta, up); err != nil {
		return nil, err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "adopt", RepoID: r.ID, Detail: r.Source.Key()})
	return l.Get(ctx, r.ID)
}

// UpstreamOf reads upstream.json of a repository.
func (l *Library) UpstreamOf(ctx context.Context, ref string) (Upstream, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return Upstream{}, err
	}
	return r.Upstream, nil
}

// RawRepoFile returns repo.yaml as stored.
func (l *Library) RawRepoFile(ctx context.Context, ref string) ([]byte, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(r.Paths.RepoFile)
}

// FetchFromPeer updates the bare repository of ref from another RepoShelf
// instance with the same force-push protection as a normal sync.
func (l *Library) FetchFromPeer(ctx context.Context, ref, url string, auth *gitcli.Auth, actor string) (gitcli.MirrorResult, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return gitcli.MirrorResult{}, err
	}
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	if _, err := os.Stat(r.Paths.Mirror); os.IsNotExist(err) {
		if err := os.MkdirAll(parentDir(r.Paths.Mirror), dirPerm); err != nil {
			return gitcli.MirrorResult{}, err
		}
		if err := l.Git.InitBare(ctx, r.Paths.Mirror, r.R().Source.URL); err != nil {
			return gitcli.MirrorResult{}, err
		}
	}
	res, err := l.Git.MirrorFrom(ctx, r.Paths.Mirror, url, auth, l.now())
	if err != nil {
		return res, err
	}
	if res.DefaultBranch != "" {
		_ = l.Git.SetHead(ctx, r.Paths.Mirror, res.DefaultBranch)
	}
	l.refreshStats(ctx, r)
	l.Record(Activity{Actor: actorOr(actor), Action: "peer_fetch", RepoID: r.R().ID,
		Detail: fmt.Sprintf("%d neu, %d aktualisiert, %d umgeschrieben", len(res.New), len(res.Updated), len(res.Rewritten))})
	return res, nil
}

// StoreRelease takes over a release from a paired instance: missing asset
// files are fetched with get and checked against their SHA-256.
func (l *Library) StoreRelease(ctx context.Context, ref string, sr StoredRelease, get func(StoredAsset) (io.ReadCloser, error)) (int, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(r.Paths.Releases, encodeName(sr.Tag))
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return 0, err
	}
	local, _ := readRelease(dir)
	n := 0
	var stored []StoredAsset
	for _, a := range sr.Stored {
		if slices.ContainsFunc(local.Stored, func(x StoredAsset) bool { return x.SHA256 == a.SHA256 && x.Name == a.Name }) {
			stored = append(stored, a)
			continue
		}
		if a.SameAs != "" {
			stored = append(stored, a)
			continue
		}
		rc, err := get(a)
		if err != nil {
			return n, err
		}
		h := sha256.New()
		target := filepath.Join(dir, encodeName(a.Name))
		err = atomicfile.Write(target, filePerm, func(w io.Writer) error {
			_, err := io.Copy(io.MultiWriter(w, h), rc)
			return err
		})
		rc.Close()
		if err != nil {
			return n, err
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
			os.Remove(target) // our own download
			return n, fmt.Errorf("%s: checksum mismatch", a.Name)
		}
		a.File = encodeName(a.Name)
		stored = append(stored, a)
		n++
	}
	sr.Stored = stored
	sr.Synced = l.now().UTC()
	if err := writeRelease(dir, sr); err != nil {
		return n, err
	}
	return n, nil
}

// WriteNotesRaw replaces notes.md without relation suggestions (sync).
func (l *Library) WriteNotesRaw(ctx context.Context, ref, text string) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(r.Paths.Meta, layout.NotesFile), []byte(text), filePerm)
}
