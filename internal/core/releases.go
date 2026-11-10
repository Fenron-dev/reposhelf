// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ReleaseFile is the manifest of one release folder.
const ReleaseFile = "release.json"

// StoredRelease is the content of release.json.
type StoredRelease struct {
	sources.Release
	Dir    string        `json:"-"`
	Stored []StoredAsset `json:"stored"`
	Synced time.Time     `json:"synced"`
}

// StoredAsset is a downloaded asset.
type StoredAsset struct {
	Name   string `json:"name"`
	File   string `json:"file"` // file name in the release folder
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// SameAs names another release folder holding the identical file when
	// it could not be hard-linked (deduplication, KONZEPT 6.4).
	SameAs string `json:"same_as,omitempty"`
}

// ReleaseReport sums up a release sync.
type ReleaseReport struct {
	Releases   int
	Downloaded int
	Bytes      int64
	Deduped    int
	// Skipped lists assets above the size limit.
	Skipped []string
}

// encodeName turns a version or asset name from the network into a safe
// folder or file name. "/" and characters forbidden on Windows become %XX,
// so "release/1.0" is stored as "release%2F1.0" and stays reversible.
func encodeName(name string) string {
	var b strings.Builder
	for i, r := range name {
		last := i == len(name)-len(string(r))
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|%`, r),
			last && (r == '.' || r == ' '), i == 0 && r == ' ':
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			b.WriteRune(r)
		}
	}
	s := b.String()
	if safepath.CheckSegment(s) != nil {
		s = "_" + s
	}
	if len(s) > safepath.MaxSegmentBytes {
		sum := sha256.Sum256([]byte(name))
		s = s[:200] + "~" + hex.EncodeToString(sum[:8])
	}
	return s
}

// EstimateReleaseSize sums the asset sizes RepoShelf would download for
// the newest keep releases (shown in the add dialog, T-1.13).
func EstimateReleaseSize(rels []sources.Release, keep int, maxAssetMB int) (int64, int) {
	sort.SliceStable(rels, func(i, j int) bool { return timeOf(rels[i].Published).After(timeOf(rels[j].Published)) })
	var total int64
	files, n := 0, 0
	limit := int64(maxAssetMB) << 20
	for _, r := range rels {
		if r.Draft || n >= keep {
			continue
		}
		n++
		for _, a := range r.Assets {
			if limit > 0 && a.Size > limit {
				continue
			}
			total += a.Size
			files++
		}
	}
	return total, files
}

// releasesKeep returns how many releases keep their assets.
func (l *Library) releasesKeep(r *Repo) int {
	return l.EffectiveRetention(r).ReleasesKeep
}

func (l *Library) syncReleases(ctx context.Context, r *Repo) (ReleaseReport, error) {
	var rep ReleaseReport
	api, err := l.Sources.API(r.R().Source.Host)
	if err != nil {
		return rep, nil // generic git host: versions are git tags only
	}
	rels, err := api.Releases(ctx, r.R().Source)
	if err != nil {
		return rep, err
	}
	sort.SliceStable(rels, func(i, j int) bool { return timeOf(rels[i].Published).After(timeOf(rels[j].Published)) })
	keep := l.releasesKeep(r)
	pinned := r.R().Retention.Pinned
	known := l.knownHashes(r)

	if err := os.MkdirAll(r.Paths.Releases, dirPerm); err != nil {
		return rep, err
	}
	kept := 0
	for _, rel := range rels {
		if rel.Draft {
			continue
		}
		rep.Releases++
		dir := filepath.Join(r.Paths.Releases, encodeName(rel.Tag))
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return rep, err
		}
		old, _ := readRelease(dir)
		sr := StoredRelease{Release: rel, Synced: l.now().UTC(), Stored: old.Stored}
		isPinned := slices.Contains(pinned, rel.Tag)
		wantAssets := isPinned || kept < keep
		if wantAssets && !isPinned {
			kept++
		}
		if wantAssets {
			for _, a := range rel.Assets {
				if slices.ContainsFunc(sr.Stored, func(s StoredAsset) bool { return s.Name == a.Name }) {
					continue
				}
				if limit := int64(l.Config().Retention.MaxAssetMB) << 20; limit > 0 && a.Size > limit {
					rep.Skipped = append(rep.Skipped, rel.Tag+"/"+a.Name)
					continue
				}
				l.progress(r.R().ID, "Releases: "+rel.Tag+" / "+a.Name, -1)
				sa, n, deduped, err := l.storeAsset(ctx, api, dir, a, known)
				if err != nil {
					return rep, fmt.Errorf("%s/%s: %w", rel.Tag, a.Name, err)
				}
				sr.Stored = append(sr.Stored, sa)
				known[sa.SHA256] = filepath.Join(dir, sa.File)
				rep.Bytes += n
				if deduped {
					rep.Deduped++
				} else {
					rep.Downloaded++
				}
			}
		}
		if err := writeRelease(dir, sr); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// storeAsset downloads a into dir, verifying the host's digest, and
// deduplicates against known (sha256 → path).
func (l *Library) storeAsset(ctx context.Context, api sources.API, dir string, a sources.Asset, known map[string]string) (StoredAsset, int64, bool, error) {
	sa := StoredAsset{Name: a.Name, File: encodeName(a.Name), Size: a.Size}
	target := filepath.Join(dir, sa.File)
	if want, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
		if existing, dup := known[want]; dup {
			sa.SHA256 = want
			return sa, 0, true, linkOrRef(existing, target, &sa, dir)
		}
	}
	h := sha256.New()
	var n int64
	err := atomicfile.Write(target, filePerm, func(w io.Writer) error {
		cw := &countWriter{w: io.MultiWriter(w, h)}
		err := api.Download(ctx, a, cw)
		n = cw.n
		return err
	})
	if err != nil {
		return sa, 0, false, err
	}
	sa.SHA256 = hex.EncodeToString(h.Sum(nil))
	sa.Size = n
	if want, ok := strings.CutPrefix(a.Digest, "sha256:"); ok && want != sa.SHA256 {
		os.Remove(target) // our own download, never kept when corrupt
		return sa, 0, false, fmt.Errorf("checksum mismatch (expected %s, got %s)", want, sa.SHA256)
	}
	if existing, dup := known[sa.SHA256]; dup && existing != target {
		os.Remove(target)
		return sa, n, true, linkOrRef(existing, target, &sa, dir)
	}
	return sa, n, false, nil
}

// linkOrRef hard-links an identical file; where the file system cannot
// (FAT/exFAT), the manifest refers to the other copy instead.
func linkOrRef(existing, target string, sa *StoredAsset, dir string) error {
	if err := os.Link(existing, target); err == nil {
		return nil
	}
	rel, err := filepath.Rel(filepath.Dir(dir), filepath.Dir(existing))
	if err != nil {
		return err
	}
	sa.SameAs = filepath.ToSlash(rel)
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (l *Library) knownHashes(r *Repo) map[string]string {
	m := map[string]string{}
	for _, rel := range l.listReleases(r) {
		for _, a := range rel.Stored {
			if a.SameAs == "" {
				m[a.SHA256] = filepath.Join(rel.Dir, a.File)
			}
		}
	}
	return m
}

func readRelease(dir string) (StoredRelease, error) {
	var sr StoredRelease
	b, err := os.ReadFile(filepath.Join(dir, ReleaseFile))
	if err != nil {
		return sr, err
	}
	err = json.Unmarshal(b, &sr)
	sr.Dir = dir
	return sr, err
}

func writeRelease(dir string, sr StoredRelease) error {
	b, err := json.MarshalIndent(sr, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(filepath.Join(dir, ReleaseFile), append(b, '\n'), filePerm); err != nil {
		return err
	}
	if sr.Body != "" {
		return atomicfile.WriteFile(filepath.Join(dir, "notes.md"), []byte(sr.Body), filePerm)
	}
	return nil
}

// listReleases reads all release manifests of r, newest first.
func (l *Library) listReleases(r *Repo) []StoredRelease {
	entries, err := os.ReadDir(r.Paths.Releases)
	if err != nil {
		return nil
	}
	var out []StoredRelease
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if sr, err := readRelease(filepath.Join(r.Paths.Releases, e.Name())); err == nil {
			out = append(out, sr)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return timeOf(out[i].Published).After(timeOf(out[j].Published)) })
	return out
}

// Releases returns the stored releases of a repository.
func (l *Library) Releases(ctx context.Context, ref string) ([]StoredRelease, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return l.listReleases(r), nil
}

// AssetPath returns the file of a stored asset (for downloads), checked to
// lie inside the release folder.
func (l *Library) AssetPath(ctx context.Context, ref, tag, name string) (string, error) {
	rels, err := l.Releases(ctx, ref)
	if err != nil {
		return "", err
	}
	for _, rel := range rels {
		if rel.Tag != tag {
			continue
		}
		for _, a := range rel.Stored {
			if a.Name != name {
				continue
			}
			dir := rel.Dir
			if a.SameAs != "" {
				dir = filepath.Join(filepath.Dir(rel.Dir), filepath.FromSlash(a.SameAs))
			}
			p, err := safepath.Join(dir, a.File)
			if err != nil {
				return "", err
			}
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("%s: %w", name, fs.ErrNotExist)
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("%s %s: %w", tag, name, fs.ErrNotExist)
}

func (l *Library) countReleases(r *Repo) int {
	entries, err := os.ReadDir(r.Paths.Releases)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(r.Paths.Releases, e.Name(), ReleaseFile)); e.IsDir() && !errors.Is(err, fs.ErrNotExist) {
			n++
		}
	}
	return n
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
