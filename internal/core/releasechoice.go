// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/assetclass"
	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- Choosing release files before adding (D-035, T-9.27) ----
//
// The add dialog shows which systems, processors and formats a project
// offers and how much the chosen profile would download. Every change in
// the dialog asks again; the release list is kept for a few minutes so
// that does not cost API calls (60/h without token on GitHub).

const (
	releaseCacheTTL = 10 * time.Minute
	releaseCacheMax = 50
	// maxPreviewReleases is how many releases the dialog lists to pin.
	maxPreviewReleases = 100
)

type cachedReleases struct {
	at   time.Time
	rels []sources.Release
}

var releaseCache = struct {
	sync.Mutex
	m map[string]cachedReleases
}{m: map[string]cachedReleases{}}

// previewReleases lists the releases of src, newest first, from the cache
// when it is fresh.
func (l *Library) previewReleases(ctx context.Context, api sources.API, src repometa.Source) ([]sources.Release, error) {
	key := src.Key()
	releaseCache.Lock()
	c, ok := releaseCache.m[key]
	releaseCache.Unlock()
	if ok && l.now().Sub(c.at) < releaseCacheTTL {
		return c.rels, nil
	}
	rels, err := api.Releases(ctx, src)
	if err != nil {
		return nil, err
	}
	rels = slices.DeleteFunc(rels, func(r sources.Release) bool { return r.Draft })
	sort.SliceStable(rels, func(i, j int) bool { return timeOf(rels[i].Published).After(timeOf(rels[j].Published)) })
	releaseCache.Lock()
	if len(releaseCache.m) >= releaseCacheMax {
		releaseCache.m = map[string]cachedReleases{}
	}
	releaseCache.m[key] = cachedReleases{at: l.now(), rels: rels}
	releaseCache.Unlock()
	return rels, nil
}

// ReleaseChoice is what the add dialog chose. Zero = the default profile.
type ReleaseChoice struct {
	// Files is the choice for repo.yaml (nil = default profile).
	Files *repometa.ReleaseFiles
	// Keep overrides the number of releases (nil = profile or global).
	Keep *int
	// Pinned versions keep their files like favourites.
	Pinned []string
}

// resolve returns the number of releases and the file filter of a
// choice, as EffectiveRetention would for a new repository.
func (l *Library) resolve(c ReleaseChoice) (keep int, files assetclass.Filter, profile string) {
	cfg := l.Config()
	keep = cfg.Retention.ReleasesKeep
	rf := c.Files
	if rf != nil && rf.Profile == "" && (len(rf.OS) > 0 || len(rf.Arch) > 0 || len(rf.Formats) > 0) {
		files = rf.Filter()
	} else {
		name := cfg.DefaultReleaseProfile
		if rf != nil && rf.Profile != "" {
			name = rf.Profile
		}
		p, ok := cfg.Profile(name)
		if !ok {
			p, _ = cfg.Profile(config.StandardProfile)
		}
		profile, files = p.Name, p.Filter()
		if rf != nil {
			files.Include = append(slices.Clone(files.Include), rf.Include...)
			files.Exclude = append(slices.Clone(files.Exclude), rf.Exclude...)
		}
		if p.ReleasesKeep != nil {
			keep = *p.ReleasesKeep
		}
	}
	if c.Keep != nil {
		keep = *c.Keep
	}
	return keep, files, profile
}

// ReleaseEstimate is the download a choice would cause.
type ReleaseEstimate struct {
	Keep    int
	Profile string
	Bytes   int64
	Files   int
	// AllBytes and AllFiles are the same releases without a choice of
	// files, to show what the choice saves.
	AllBytes int64
	AllFiles int
}

// EstimateChoice computes what a choice would download from rels.
func (l *Library) EstimateChoice(rels []sources.Release, c ReleaseChoice) ReleaseEstimate {
	keep, files, profile := l.resolve(c)
	maxMB := l.Config().Retention.MaxAssetMB
	e := ReleaseEstimate{Keep: keep, Profile: profile}
	e.Bytes, e.Files = EstimateReleaseSize(slices.Clone(rels), keep, maxMB, files, c.Pinned)
	e.AllBytes, e.AllFiles = EstimateReleaseSize(slices.Clone(rels), keep, maxMB, assetclass.Filter{}, c.Pinned)
	return e
}

// ValidateChoice checks a choice before a repository is created with it.
func (l *Library) ValidateChoice(c ReleaseChoice) error {
	if c.Keep != nil && *c.Keep < 0 {
		return fmt.Errorf("%w: Anzahl der Releases darf nicht negativ sein", ErrInvalid)
	}
	if rf := c.Files; rf != nil && rf.Profile != "" {
		if _, ok := l.Config().Profile(rf.Profile); !ok {
			return fmt.Errorf("%w: Release-Profil %q gibt es nicht", ErrInvalid, rf.Profile)
		}
	}
	return nil
}

// SetReleaseFiles changes the choice of release files and the number of
// releases of a repository (nil: default profile, inherit). Files that
// no longer match are not touched: UnwantedFiles lists them for the
// dialog that follows (D-035).
func (l *Library) SetReleaseFiles(ctx context.Context, ref string, rf *repometa.ReleaseFiles, keep *int, actor string) (*Repo, error) {
	if err := l.ValidateChoice(ReleaseChoice{Files: rf, Keep: keep}); err != nil {
		return nil, err
	}
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		r.ReleaseFiles = rf
		r.Retention.ReleasesKeep = keep
		return r.Validate()
	})
	var fe *repometa.FieldError
	if errors.As(err, &fe) {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err != nil {
		return nil, err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "release-files", RepoID: r.R().ID})
	return r, nil
}

// ReleasesOf lists the releases behind a link, newest first (cached for
// the add dialog).
func (l *Library) ReleasesOf(ctx context.Context, link string) ([]sources.Release, error) {
	p, err := sources.ParseURL(link)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	api, err := l.Sources.API(p.Source.Host)
	if err != nil {
		return nil, err
	}
	return l.previewReleases(ctx, api, p.Source)
}

// EstimateLink computes what a choice would download for a link.
func (l *Library) EstimateLink(ctx context.Context, link string, c ReleaseChoice) (ReleaseEstimate, error) {
	rels, err := l.ReleasesOf(ctx, link)
	if err != nil {
		return ReleaseEstimate{}, err
	}
	return l.EstimateChoice(rels, c), nil
}

// KnownReleases returns the releases RepoShelf knows of a repository
// (with all their files, also those not downloaded), newest first.
func (l *Library) KnownReleases(ctx context.Context, ref string) ([]sources.Release, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []sources.Release
	for _, sr := range l.listReleases(r) {
		out = append(out, sr.Release)
	}
	return out, nil
}

// ProfileUsage counts the repositories per release profile (those without
// a choice under the default profile's name).
func (l *Library) ProfileUsage(ctx context.Context) map[string]int {
	out := map[string]int{}
	entries, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return out
	}
	for _, e := range entries {
		r, err := l.load(ctx, e.MetaPath)
		if err != nil {
			continue
		}
		if p, own := l.releaseProfile(r); !own {
			out[p.Name]++
		}
	}
	return out
}

// UnwantedCount counts the releases with stored files that no longer
// match their repository's choice.
func (l *Library) UnwantedCount(ctx context.Context) int {
	plan, err := l.CleanupPlan(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, it := range plan {
		if it.Kind == ItemReleaseFiles {
			n++
		}
	}
	return n
}
