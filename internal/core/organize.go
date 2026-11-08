// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- Tags (T-2.1) ----

// SetTags replaces the tags of a repository.
func (l *Library) SetTags(ctx context.Context, ref string, tags []string, actor string) (*Repo, error) {
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		r.Tags = cleanList(tags)
		return nil
	})
	if err == nil {
		l.Record(Activity{Actor: actorOr(actor), Action: "tags", RepoID: r.R().ID, Detail: strings.Join(r.R().Tags, ", ")})
	}
	return r, err
}

// RenameTag renames (or, if to exists, merges) a tag in every repository,
// including all sub-tags: "ttrpg" → "rpg" turns "ttrpg/tool" into
// "rpg/tool" (KONZEPT 6.6). Each repo.yaml is rewritten atomically.
func (l *Library) RenameTag(ctx context.Context, from, to, actor string) (int, error) {
	from, to = strings.Trim(from, "/ "), strings.Trim(to, "/ ")
	if from == "" || to == "" {
		return 0, fmt.Errorf("%w: empty tag", ErrInvalid)
	}
	entries, err := l.Store.List(ctx, store.Query{Tag: from + "/*"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		_, err := l.Update(ctx, e.ID, func(r *repometa.Repo) error {
			var out []string
			for _, t := range r.Tags {
				switch {
				case t == from:
					t = to
				case strings.HasPrefix(t, from+"/"):
					t = to + strings.TrimPrefix(t, from)
				}
				if !slices.Contains(out, t) {
					out = append(out, t)
				}
			}
			r.Tags = out
			return nil
		})
		if err != nil {
			return n, fmt.Errorf("%s: %w", e.Key, err)
		}
		n++
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "rename_tag", Detail: from + " → " + to})
	return n, nil
}

// TagNode is a tag in the hierarchy with its own and total counts.
type TagNode struct {
	Name     string // last level
	Path     string // full tag
	Count    int    // repositories with exactly this tag
	Total    int    // including sub-tags
	Children []*TagNode
}

// TagTree returns all tags as a hierarchy (ttrpg → tool, plugin).
func (l *Library) TagTree(ctx context.Context) ([]*TagNode, error) {
	counts, err := l.Store.Tags(ctx)
	if err != nil {
		return nil, err
	}
	root := &TagNode{}
	for _, c := range counts {
		node := root
		parts := strings.Split(c.Name, "/")
		for i, p := range parts {
			var child *TagNode
			for _, ch := range node.Children {
				if ch.Name == p {
					child = ch
				}
			}
			if child == nil {
				child = &TagNode{Name: p, Path: strings.Join(parts[:i+1], "/")}
				node.Children = append(node.Children, child)
			}
			child.Total += c.Count
			node = child
		}
		node.Count = c.Count
	}
	return root.Children, nil
}

// ---- Collections (T-2.2, T-2.4) ----

// Collection is a manual or smart collection, stored as
// collections/<id>.yaml.
type Collection struct {
	ID          string `yaml:"-"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Color       string `yaml:"color"`
	Icon        string `yaml:"icon"`
	// Smart collections hold a saved filter in the {logic, rules} format of
	// MediaShelf/OracleVault; members are computed, not stored.
	Smart *SmartFilter `yaml:"smart"`
	// Retention overrides the global rules for members (KONZEPT 6.10).
	Retention CollectionRetention `yaml:"retention"`
	Created   time.Time           `yaml:"created"`
	// Size is filled from the index.
	Size int `yaml:"-"`
}

// CollectionRetention are optional overrides; nil inherits.
type CollectionRetention struct {
	ReleasesKeep            *int               `yaml:"releases_keep"`
	SnapshotsEvery          *repometa.Duration `yaml:"snapshots_every"`
	SnapshotsKeepFor        *repometa.Duration `yaml:"snapshots_keep_for"`
	ForcePushBackupsKeepFor *repometa.Duration `yaml:"force_push_backups_keep_for"`
	CapsulesKeep            *int               `yaml:"capsules_keep"`
}

func (l *Library) collectionPath(id string) (string, error) {
	if err := safepath.CheckSegment(id); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return safepath.Join(l.Layout.Path(layout.DirCollections), id+".yaml")
}

// Collections lists all collections with their sizes.
func (l *Library) Collections(ctx context.Context) ([]Collection, error) {
	dir := l.Layout.Path(layout.DirCollections)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sizes := map[string]int{}
	if counts, err := l.Store.Collections(ctx); err == nil {
		for _, c := range counts {
			sizes[c.Name] = c.Count
		}
	}
	seen := map[string]bool{}
	var out []Collection
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok || e.IsDir() {
			continue
		}
		c, err := l.Collection(ctx, id)
		if err != nil {
			l.Log.Warn("collection unreadable", "id", id, "err", err)
			continue
		}
		out = append(out, c)
		seen[id] = true
	}
	// Collections used in repo.yaml without a file still show up.
	for id, n := range sizes {
		if !seen[id] {
			out = append(out, Collection{ID: id, Name: id, Size: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Collection reads one collection (a missing file gives a bare one).
func (l *Library) Collection(ctx context.Context, id string) (Collection, error) {
	p, err := l.collectionPath(id)
	if err != nil {
		return Collection{}, err
	}
	c := Collection{ID: id, Name: id}
	raw, err := os.ReadFile(p)
	if err == nil {
		f, err := repometa.ParseFile(raw, c)
		if err != nil {
			return c, fmt.Errorf("%s: %w", p, err)
		}
		c = f.Value
		c.ID = id
	} else if !errors.Is(err, fs.ErrNotExist) {
		return c, err
	}
	if c.Smart != nil {
		if members, err := l.SmartMembers(ctx, *c.Smart); err == nil {
			c.Size = len(members)
		}
	} else if counts, err := l.Store.Collections(ctx); err == nil {
		for _, n := range counts {
			if n.Name == id {
				c.Size = n.Count
			}
		}
	}
	return c, nil
}

// SaveCollection creates or updates collections/<id>.yaml, keeping unknown
// keys and comments.
func (l *Library) SaveCollection(c Collection, actor string) error {
	p, err := l.collectionPath(c.ID)
	if err != nil {
		return err
	}
	if c.Smart != nil {
		if err := c.Smart.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if c.Created.IsZero() {
		c.Created = l.now().UTC().Truncate(time.Second)
	}
	f := repometa.NewFile(c)
	if raw, err := os.ReadFile(p); err == nil {
		if parsed, err := repometa.ParseFile(raw, Collection{}); err == nil {
			parsed.Value = c
			f = parsed
		}
	}
	b, err := f.Marshal()
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(p, b, filePerm); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "collection", Detail: c.ID})
	l.events.publish(Event{Kind: "collection"})
	return nil
}

// SetCollections replaces the manual collections of a repository.
func (l *Library) SetCollections(ctx context.Context, ref string, ids []string, actor string) (*Repo, error) {
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		r.Collections = cleanList(ids)
		return nil
	})
	if err == nil {
		l.Record(Activity{Actor: actorOr(actor), Action: "collections", RepoID: r.R().ID, Detail: strings.Join(r.R().Collections, ", ")})
	}
	return r, err
}

// CollectionMembers lists the repositories of a collection (smart or manual).
func (l *Library) CollectionMembers(ctx context.Context, id string) ([]store.Entry, error) {
	c, err := l.Collection(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.Smart != nil {
		return l.SmartMembers(ctx, *c.Smart)
	}
	return l.Store.List(ctx, store.Query{Collection: id})
}

// ---- Notes (T-2.6) ----

// Notes returns notes.md of a repository.
func (l *Library) Notes(ctx context.Context, ref string) (string, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(r.Paths.Meta, layout.NotesFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// WriteNotes replaces notes.md and refreshes wiki-link relations.
func (l *Library) WriteNotes(ctx context.Context, ref, text, actor string) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	if err := atomicfile.WriteFile(filepath.Join(r.Paths.Meta, layout.NotesFile), []byte(text), filePerm); err != nil {
		return err
	}
	if r, err = l.load(ctx, r.Paths.Meta); err != nil {
		return err
	}
	l.suggestRelations(ctx, r)
	if err := l.save(ctx, r); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "notes", RepoID: r.R().ID})
	return nil
}

// ---- Relations (T-2.3) ----

// SetRelation adds or updates a relation (state confirmed, suggested or
// rejected).
func (l *Library) SetRelation(ctx context.Context, ref, typ, target string, state repometa.RelationState, actor string) (*Repo, error) {
	if t, err := l.Store.Get(ctx, target); err == nil {
		target = t.Key
	}
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		for i, rel := range r.Relations {
			if rel.Type == typ && strings.EqualFold(rel.Target, target) {
				r.Relations[i].State = state
				return nil
			}
		}
		r.Relations = append(r.Relations, repometa.Relation{Type: typ, Target: target, State: state, Source: "manual"})
		return nil
	})
	if err == nil {
		l.Record(Activity{Actor: actorOr(actor), Action: "relation", RepoID: r.R().ID, Detail: fmt.Sprintf("%s %s (%s)", typ, target, state)})
	}
	return r, err
}

// RemoveRelation deletes a relation entry from repo.yaml. Rejecting is
// usually better (it stops the suggestion from coming back).
func (l *Library) RemoveRelation(ctx context.Context, ref, typ, target, actor string) (*Repo, error) {
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		r.Relations = slices.DeleteFunc(r.Relations, func(rel repometa.Relation) bool {
			return rel.Type == typ && strings.EqualFold(rel.Target, target)
		})
		return nil
	})
	if err == nil {
		l.Record(Activity{Actor: actorOr(actor), Action: "relation_remove", RepoID: r.R().ID, Detail: typ + " " + target})
	}
	return r, err
}

// Graph is the relation graph for the graph view (T-9.4).
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is a repository.
type GraphNode struct {
	ID, Label, Kind, Status string
}

// GraphEdge is a confirmed or suggested relation.
type GraphEdge struct {
	From, To, Type, State string
}

// Graph returns all repositories with relations between them.
func (l *Library) Graph(ctx context.Context) (Graph, error) {
	all, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return Graph{}, err
	}
	var g Graph
	byRef := map[string]string{}
	for _, e := range all {
		byRef[e.ID] = e.ID
		byRef[store.FoldKey(e.Key)] = e.ID
	}
	linked := map[string]bool{}
	for _, e := range all {
		for _, rel := range e.Relations {
			to, ok := byRef[rel.To]
			if !ok {
				to, ok = byRef[store.FoldKey(rel.To)]
			}
			if !ok || rel.State == string(repometa.RelationRejected) {
				continue
			}
			g.Edges = append(g.Edges, GraphEdge{From: e.ID, To: to, Type: rel.Type, State: rel.State})
			linked[e.ID], linked[to] = true, true
		}
	}
	for _, e := range all {
		if linked[e.ID] {
			g.Nodes = append(g.Nodes, GraphNode{ID: e.ID, Label: e.Owner + "/" + e.Name, Kind: e.Kind, Status: e.Status})
		}
	}
	return g, nil
}

// ---- Repository settings ----

// Settings are the user-editable parts of repo.yaml.
type Settings struct {
	Kind           repometa.Kind
	Mode           repometa.SyncMode
	Interval       *repometa.Duration
	Include        repometa.Include
	Buildable      bool
	StatusOverride *repometa.UpstreamStatus
	Retention      repometa.Retention
}

// UpdateSettings changes sync, include, buildable, override and retention.
func (l *Library) UpdateSettings(ctx context.Context, ref string, s Settings, actor string) (*Repo, error) {
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		if s.Kind != "" && s.Kind != r.Kind {
			if s.Kind == repometa.KindFork || r.Kind == repometa.KindFork {
				return fmt.Errorf("%w: forks are created with Fork", ErrInvalid)
			}
			r.Kind = s.Kind
		}
		r.Sync.Mode = s.Mode
		r.Sync.Interval = s.Interval
		if s.Mode == repometa.SyncFollow && r.Sync.Interval == nil {
			d := l.Config().Sync.DefaultInterval
			r.Sync.Interval = &d
		}
		r.Include = s.Include
		r.Buildable.Enabled = s.Buildable
		r.StatusOverride = s.StatusOverride
		r.Retention = s.Retention
		return r.Validate()
	})
	if err != nil {
		var fe *repometa.FieldError
		if errors.As(err, &fe) {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return nil, err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "settings", RepoID: r.R().ID})
	return r, nil
}

// ---- Effective retention (KONZEPT 6.10: global → collection → repo) ----

// Retention is the effective rule set of one repository.
type Retention struct {
	ReleasesKeep            int
	Pinned                  []string
	SnapshotsEvery          repometa.Duration
	SnapshotsKeepFor        repometa.Duration
	ForcePushBackupsKeepFor repometa.Duration
	CapsulesKeep            int
	// From names where each value came from (for the UI).
	From map[string]string
}

// EffectiveRetention resolves the rules for r: the repository's own value
// wins, then the first of its collections that sets one, then the global
// configuration.
func (l *Library) EffectiveRetention(r *Repo) Retention {
	g := l.Config().Retention
	out := Retention{
		ReleasesKeep: g.ReleasesKeep, SnapshotsEvery: g.SnapshotsEvery, SnapshotsKeepFor: g.SnapshotsKeepFor,
		ForcePushBackupsKeepFor: g.ForcePushBackupsKeepFor, CapsulesKeep: g.CapsulesKeep,
		Pinned: r.R().Retention.Pinned,
		From:   map[string]string{"releases_keep": "global", "snapshots": "global", "force_push_backups": "global", "capsules_keep": "global"},
	}
	cols := slices.Clone(r.R().Collections)
	sort.Strings(cols)
	for i := len(cols) - 1; i >= 0; i-- { // first collection (sorted) wins
		c, err := l.Collection(context.Background(), cols[i])
		if err != nil {
			continue
		}
		cr := c.Retention
		if cr.ReleasesKeep != nil {
			out.ReleasesKeep, out.From["releases_keep"] = *cr.ReleasesKeep, "Sammlung "+c.Name
		}
		if cr.SnapshotsEvery != nil {
			out.SnapshotsEvery, out.From["snapshots"] = *cr.SnapshotsEvery, "Sammlung "+c.Name
		}
		if cr.SnapshotsKeepFor != nil {
			out.SnapshotsKeepFor = *cr.SnapshotsKeepFor
		}
		if cr.ForcePushBackupsKeepFor != nil {
			out.ForcePushBackupsKeepFor, out.From["force_push_backups"] = *cr.ForcePushBackupsKeepFor, "Sammlung "+c.Name
		}
		if cr.CapsulesKeep != nil {
			out.CapsulesKeep, out.From["capsules_keep"] = *cr.CapsulesKeep, "Sammlung "+c.Name
		}
	}
	rr := r.R().Retention
	if rr.ReleasesKeep != nil {
		out.ReleasesKeep, out.From["releases_keep"] = *rr.ReleasesKeep, "Repo"
	}
	if rr.Snapshots.Every != nil {
		out.SnapshotsEvery, out.From["snapshots"] = *rr.Snapshots.Every, "Repo"
	}
	if rr.Snapshots.KeepFor != nil {
		out.SnapshotsKeepFor = *rr.Snapshots.KeepFor
	}
	if rr.ForcePushBackups.KeepFor != nil {
		out.ForcePushBackupsKeepFor, out.From["force_push_backups"] = *rr.ForcePushBackups.KeepFor, "Repo"
	}
	if r.R().Buildable.CapsulesKeep != nil {
		out.CapsulesKeep, out.From["capsules_keep"] = *r.R().Buildable.CapsulesKeep, "Repo"
	}
	return out
}

// Pin adds a version to retention.pinned (T-3.2): its assets, snapshots
// and capsules are never cleaned up.
func (l *Library) Pin(ctx context.Context, ref, version string, pin bool, actor string) (*Repo, error) {
	r, err := l.Update(ctx, ref, func(r *repometa.Repo) error {
		has := slices.Contains(r.Retention.Pinned, version)
		switch {
		case pin && !has:
			r.Retention.Pinned = append(r.Retention.Pinned, version)
		case !pin && has:
			r.Retention.Pinned = slices.DeleteFunc(r.Retention.Pinned, func(s string) bool { return s == version })
		}
		return nil
	})
	if err == nil {
		action := "pin"
		if !pin {
			action = "unpin"
		}
		l.Record(Activity{Actor: actorOr(actor), Action: action, RepoID: r.R().ID, Detail: version})
	}
	return r, err
}
