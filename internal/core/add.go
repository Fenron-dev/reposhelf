// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/assetclass"
	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Preview is what the add dialog shows before anything is written.
type Preview struct {
	Parsed   sources.Parsed
	Metadata *sources.Metadata
	// MetaError explains why metadata is missing (no API, rate limit …).
	MetaError error
	// Existing is set when the folder is already used (D-018, T-1.12).
	Existing *store.Entry
	// SuggestedTags come from the topics.
	SuggestedTags []string
	// ReleaseBytes and ReleaseFiles estimate the release download with the
	// default retention (T-1.13); -1 if unknown.
	ReleaseBytes int64
	ReleaseFiles int
	// Releases are the newest releases (to pin some), Offer what systems,
	// processors and formats they contain (D-035); Estimate the download
	// with the default profile.
	Releases []sources.Release
	Offer    assetclass.Offer
	Estimate ReleaseEstimate
	// NoToken is set when the host does not show the repository and no
	// token is set: it may be private (T-9.10).
	NoToken bool
	// NoAccess is set when the host does not show the repository although
	// a token is set: the token lacks access to it; TokenFrom says which
	// token is in effect.
	NoAccess  bool
	TokenFrom string
	// ModelBytes is the size of a Hugging Face model (-1 unknown); Large
	// asks for a confirmation before it is added (D-025).
	ModelBytes int64
	Large      bool
}

// Preview recognises a link and fetches metadata without writing anything.
func (l *Library) Preview(ctx context.Context, link string) (Preview, error) {
	return l.preview(ctx, link, true)
}

// preview with estimate=false skips the release listing (one or more API
// calls) – adding a repository does not need it.
func (l *Library) preview(ctx context.Context, link string, estimate bool) (Preview, error) {
	p, err := sources.ParseURL(link)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if p.Source.Host == repometa.LocalHost {
		return Preview{}, fmt.Errorf("%w: host %q is reserved", ErrInvalid, repometa.LocalHost)
	}
	pv := Preview{Parsed: p, ReleaseBytes: -1, ModelBytes: -1}
	if e, err := l.Store.ByFolder(ctx, p.Source.Key()); err == nil {
		pv.Existing = &e
	}
	if api, err := l.Sources.API(p.Source.Host); err == nil {
		m, err := api.Metadata(ctx, p.Source)
		if err != nil {
			pv.MetaError = err
		} else {
			pv.Metadata = &m
			if p.Provider == sources.KindHuggingFace {
				pv.ModelBytes = m.Size
				ask := int64(l.Config().Retention.AskAboveMB) << 20
				pv.Large = ask > 0 && m.Size > ask
			}
			if estimate && p.Provider != sources.KindHuggingFace {
				if rels, err := l.previewReleases(ctx, api, p.Source); err == nil {
					pv.Estimate = l.EstimateChoice(rels, ReleaseChoice{})
					pv.ReleaseBytes, pv.ReleaseFiles = pv.Estimate.Bytes, pv.Estimate.Files
					var names []string
					for _, r := range rels {
						names = append(names, assetNames(r.Assets)...)
					}
					pv.Offer = assetclass.Describe(names)
					pv.Releases = rels[:min(len(rels), maxPreviewReleases)]
				}
			}
			for _, t := range m.Topics {
				if safepath.CheckSegment(t) == nil {
					pv.SuggestedTags = append(pv.SuggestedTags, t)
				}
			}
		}
	} else {
		pv.MetaError = err
	}
	if errors.Is(pv.MetaError, sources.ErrGone) || errors.Is(pv.MetaError, sources.ErrAuth) {
		if src := l.Tokens.Source(pv.Parsed.Source.Host); src.Kind == sources.FromNone {
			pv.NoToken = true
		} else {
			pv.NoAccess, pv.TokenFrom = true, TokenOrigin(src)
		}
	}
	return pv, nil
}

// AddRequest describes a new repository (KONZEPT 6.1).
type AddRequest struct {
	URL         string
	Kind        repometa.Kind // default mirror
	Mode        repometa.SyncMode
	Interval    *repometa.Duration
	Include     *repometa.Include
	Tags        []string
	Collections []string
	Buildable   bool
	Notes       string
	Actor       string
	// Wait runs the first sync before returning (CLI); otherwise it is
	// queued in the background (server).
	Wait bool
	// ConfirmLarge confirms downloading a model above the size limit
	// (D-025). Agents cannot set it: a person decides.
	ConfirmLarge bool
	// Releases chooses the release files, the number of releases and
	// pinned versions (D-035); zero = the default profile.
	Releases ReleaseChoice
	// seed fills the new repository before its first sync (files of a
	// local folder, T-9.17).
	seed func(*Repo) error
}

// ErrTooLarge is returned when a large download needs a confirmation.
var ErrTooLarge = errors.New("needs confirmation because of its size")

// Add registers a repository: repo.yaml, upstream.json and an empty bare
// repository are written at once; the first sync follows.
func (l *Library) Add(ctx context.Context, req AddRequest) (*Repo, error) {
	pv, err := l.preview(ctx, req.URL, false)
	if err != nil {
		return nil, err
	}
	if pv.Existing != nil {
		return nil, fmt.Errorf("%w: %s (%s)", ErrExists, pv.Existing.Key, pv.Existing.URL)
	}
	if pv.NoToken {
		return nil, fmt.Errorf("%w: %s – bei einem privaten Repo zuerst unter Einstellungen → Zugänge einen Token für %s hinterlegen", ErrNotFound, pv.Parsed.Source.URL, pv.Parsed.Source.Host)
	}
	if pv.Parsed.Provider == sources.KindHuggingFace && !l.lfs {
		return nil, fmt.Errorf("%w: Modelle brauchen Git LFS – bitte git-lfs installieren", ErrInvalid)
	}
	if pv.Large && !req.ConfirmLarge {
		return nil, fmt.Errorf("%w: das Modell ist etwa %.1f GB groß – bitte bestätigen", ErrTooLarge, float64(pv.ModelBytes)/(1<<30))
	}
	if pv.NoAccess {
		return nil, fmt.Errorf("%w: %s – %s zeigt dieses Repo mit dem Token %s nicht. Ist es privat, fehlt dem Token der Zugriff darauf: auf GitHub beim Token unter „Repository access“ das Repo freigeben (oder „All repositories“) und „Contents: Read-only“ setzen; dann unter Einstellungen → Zugänge „Prüfen“", ErrNotFound, pv.Parsed.Source.URL, pv.Parsed.Source.Host, pv.TokenFrom)
	}
	if errors.Is(pv.MetaError, sources.ErrGone) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, pv.Parsed.Source.URL)
	}
	kind := req.Kind
	if kind == "" {
		kind = repometa.KindMirror
	}
	if kind == repometa.KindFork {
		return nil, fmt.Errorf("%w: forks are created from a mirror (Fork)", ErrInvalid)
	}
	r := repometa.Defaults(kind)
	r.ID = repometa.NewID(l.now())
	r.Source = pv.Parsed.Source
	r.Created = l.now().UTC().Truncate(1e9)
	r.Sync.Mode = req.Mode
	if r.Sync.Mode == "" {
		r.Sync.Mode = repometa.SyncFollow
	}
	if r.Sync.Mode == repometa.SyncFollow {
		iv := req.Interval
		if iv == nil {
			d := l.Config().Sync.DefaultInterval
			iv = &d
		}
		r.Sync.Interval = iv
	}
	if req.Include != nil {
		r.Include = *req.Include
	}
	if pv.Parsed.Provider == sources.KindHuggingFace {
		// A model is its files: LFS is required, the rest does not exist.
		r.Include = repometa.Include{LFS: true}
	}
	if err := l.ValidateChoice(req.Releases); err != nil {
		return nil, err
	}
	r.ReleaseFiles = req.Releases.Files
	r.Retention.ReleasesKeep = req.Releases.Keep
	r.Retention.Pinned = cleanList(req.Releases.Pinned)
	r.Tags = cleanList(req.Tags)
	r.Collections = cleanList(req.Collections)
	r.Buildable.Enabled = req.Buildable
	if m := pv.Metadata; m != nil && m.ForkParent != "" {
		r.Relations = append(r.Relations, repometa.Relation{Type: "fork_of", Target: m.ForkParent, State: repometa.RelationSuggested, Source: "fork"})
	}

	repo, err := l.create(ctx, r, pv.Parsed, pv.Metadata, req.Notes)
	if err != nil {
		return nil, err
	}
	l.Record(Activity{Actor: actorOr(req.Actor), Action: "add", RepoID: r.ID, Detail: r.Source.URL})
	if req.seed != nil {
		if err := req.seed(repo); err != nil {
			return repo, fmt.Errorf("ordner übernehmen: %w", err)
		}
	}
	if req.Wait {
		if _, err := l.Sync(ctx, r.ID, actorOr(req.Actor)); err != nil {
			return repo, err
		}
		return l.Get(ctx, r.ID)
	}
	l.Enqueue(r.ID, r.Source.Key(), actorOr(req.Actor))
	return repo, nil
}

// create writes the files of a new repository.
func (l *Library) create(ctx context.Context, r repometa.Repo, parsed sources.Parsed, meta *sources.Metadata, notes string) (*Repo, error) {
	p, err := l.paths(r.Source)
	if err != nil {
		return nil, err
	}
	unlock := l.locks.lock("folder:" + store.FoldKey(r.Source.Key()))
	defer unlock()
	// The index may lag behind hand-made folders: check the disk as well.
	if _, err := os.Stat(p.RepoFile); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, r.Source.Key())
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := os.MkdirAll(p.Meta, dirPerm); err != nil {
		return nil, err
	}
	up := Upstream{Provider: parsed.Provider, WebURL: parsed.WebURL, Status: StatusNew}
	if meta != nil {
		up.setMetadata(*meta, l.now().UTC())
		if meta.License != "" {
			r.License.SPDX = &meta.License
			d := repometa.NewDate(l.now())
			r.License.Checked = &d
		}
	}
	if err := writeUpstream(p.Meta, up); err != nil {
		return nil, err
	}
	if notes != "" {
		if err := atomicfile.WriteFile(p.Meta+string(os.PathSeparator)+layout.NotesFile, []byte(notes), filePerm); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(parentDir(p.Mirror), dirPerm); err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.Mirror); errors.Is(err, os.ErrNotExist) {
		if err := l.Git.InitBare(ctx, p.Mirror, r.Source.URL); err != nil {
			return nil, err
		}
	}
	repo := &Repo{Doc: repometa.New(r), Upstream: up, Paths: p}
	if err := l.save(ctx, repo); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, fmt.Errorf("%w: %s", ErrExists, r.Source.Key())
		}
		return nil, err
	}
	return repo, nil
}

// CreateLocal creates an own project that exists only in RepoShelf (no
// upstream). Agents and the user push to it via Git HTTP (KONZEPT 6.14).
func (l *Library) CreateLocal(ctx context.Context, owner, name, description string, tags []string, actor string) (*Repo, error) {
	if owner == "" {
		owner = "projekte"
	}
	r := repometa.Defaults(repometa.KindOwn)
	r.ID = repometa.NewID(l.now())
	r.Source = repometa.Source{Host: repometa.LocalHost, Owner: owner, Name: name}
	r.Sync = repometa.Sync{Mode: repometa.SyncManual}
	r.Created = l.now().UTC().Truncate(1e9)
	r.Tags = cleanList(tags)
	r.Include.Releases, r.Include.LFS, r.Include.Submodules = false, false, false
	if _, err := l.Store.ByFolder(ctx, r.Source.Key()); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, r.Source.Key())
	}
	repo, err := l.create(ctx, r, sources.Parsed{Source: r.Source, Provider: "local"}, nil, "")
	if err != nil {
		return nil, err
	}
	if description != "" {
		if err := repo.Doc.SetExtra("description", description); err == nil {
			if err := l.save(ctx, repo); err != nil {
				return nil, err
			}
		}
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "create", RepoID: r.ID, Detail: r.Source.Key()})
	return l.Get(ctx, r.ID)
}

// Fork creates a writable copy of a mirror (KONZEPT 3: e.g. to continue a
// project whose original is gone). The copy keeps fork_of.
func (l *Library) Fork(ctx context.Context, ref, owner, name, actor string) (*Repo, error) {
	orig, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	if owner == "" {
		owner = orig.R().Source.Owner
	}
	if name == "" {
		name = orig.R().Source.Name
	}
	r := repometa.Defaults(repometa.KindFork)
	r.ID = repometa.NewID(l.now())
	r.Source = repometa.Source{Host: repometa.LocalHost, Owner: owner, Name: name, URL: orig.R().Source.URL}
	r.ForkOf = &orig.R().ID
	r.Sync = repometa.Sync{Mode: repometa.SyncManual}
	r.Created = l.now().UTC().Truncate(1e9)
	r.Tags = slices.Clone(orig.R().Tags)
	r.Include.Releases = false
	p, err := l.paths(r.Source)
	if err != nil {
		return nil, err
	}
	if _, err := l.Store.ByFolder(ctx, r.Source.Key()); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrExists, r.Source.Key())
	}
	if err := os.MkdirAll(parentDir(p.Mirror), dirPerm); err != nil {
		return nil, err
	}
	if _, err := l.Git.Run(ctx, "", nil, "clone", "--bare", "--quiet", "--no-hardlinks", "--", orig.Paths.Mirror, p.Mirror); err != nil {
		return nil, err
	}
	// The fork's origin is the mirror; never fetch automatically.
	_, _ = l.Git.Run(ctx, p.Mirror, nil, "remote", "remove", "origin") // no remote is fine too
	repo, err := l.create(ctx, r, sources.Parsed{Source: r.Source, Provider: "local"}, orig.Upstream.Metadata, "")
	if err != nil {
		return nil, err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "fork", RepoID: r.ID, Detail: "from " + orig.Entry.Key})
	l.refreshStats(ctx, repo)
	return l.Get(ctx, repo.R().ID)
}

func parentDir(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return p
	}
	return p[:i]
}

func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func actorOr(a string) string {
	if a == "" {
		return "cli"
	}
	return a
}
