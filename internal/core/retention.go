// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Kinds of cleanup candidates (KONZEPT 6.10).
const (
	ItemReleaseAssets = "release_assets"
	ItemBackup        = "force_push_backup"
	ItemSnapshot      = "snapshot"
	ItemCapsule       = "capsule"
	ItemBuild         = "build"
	ItemRepo          = "repo"
)

// CleanupItem is something a rule (or the user) proposes to move to the
// trash. Nothing happens until the user decides (T-3.3).
type CleanupItem struct {
	ID     string
	Kind   string
	RepoID string
	Key    string
	Label  string
	Reason string
	Size   int64
	Since  time.Time
	// Path is a folder below the data root (assets, capsules, builds, repo);
	// Ref is a ref namespace inside the mirror (backups, snapshots).
	Path string
	Ref  string
}

// cleanupState remembers decisions between starts.
type cleanupState struct {
	// Kept items are never proposed again ("Behalten").
	Kept []string `yaml:"kept"`
	// Marked repositories are proposed for deletion (manual mark).
	Marked []string `yaml:"marked"`
}

const cleanupFile = "cleanup.yaml"

func (l *Library) cleanupStatePath() string {
	return l.Layout.Path(layout.DirInternal, cleanupFile)
}

func (l *Library) readCleanupState() cleanupState {
	var st cleanupState
	if raw, err := os.ReadFile(l.cleanupStatePath()); err == nil {
		if f, err := repometa.ParseFile(raw, st); err == nil {
			st = f.Value
		}
	}
	return st
}

func (l *Library) writeCleanupState(st cleanupState) error {
	b, err := repometa.NewFile(st).Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(l.cleanupStatePath(), b, filePerm)
}

func itemID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// CleanupPlan lists what the rules would move to the trash now (T-3.5:
// "Was würde die nächste Bereinigung entfernen?"). Pinned versions and the
// git history itself are never part of it.
func (l *Library) CleanupPlan(ctx context.Context) ([]CleanupItem, error) {
	st := l.readCleanupState()
	entries, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return nil, err
	}
	now := l.now()
	var items []CleanupItem
	for _, e := range entries {
		r, err := l.load(ctx, e.MetaPath)
		if err != nil {
			continue
		}
		ret := l.EffectiveRetention(r)
		if slices.Contains(st.Marked, e.ID) {
			items = append(items, CleanupItem{ID: itemID("repo", e.ID), Kind: ItemRepo, RepoID: e.ID, Key: e.Key,
				Label: e.Key, Reason: "zum Löschen vorgemerkt", Size: e.SizeBytes, Path: r.Paths.Meta})
		}
		items = append(items, l.releaseCandidates(r, ret)...)
		items = append(items, l.refCandidates(ctx, r, gitcli.BackupNamespace, ItemBackup, time.Duration(ret.ForcePushBackupsKeepFor), now, false)...)
		items = append(items, l.refCandidates(ctx, r, SnapshotNamespace, ItemSnapshot, time.Duration(ret.SnapshotsKeepFor), now, true)...)
		items = append(items, l.capsuleCandidates(r, ret)...)
		items = append(items, l.actionCandidates(r, ret)...)
	}
	items = append(items, l.buildCandidates()...)
	items = append(items, l.blobCandidates()...)
	items = slices.DeleteFunc(items, func(it CleanupItem) bool { return slices.Contains(st.Kept, it.ID) })
	sort.SliceStable(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	return items, nil
}

func (l *Library) releaseCandidates(r *Repo, ret Retention) []CleanupItem {
	var items []CleanupItem
	kept := 0
	for _, rel := range l.listReleases(r) {
		if slices.Contains(ret.Pinned, rel.Tag) {
			continue
		}
		if kept < ret.ReleasesKeep {
			kept++
			continue
		}
		var size int64
		for _, a := range rel.Stored {
			if a.SameAs == "" {
				size += a.Size
			}
		}
		if len(rel.Stored) == 0 {
			continue
		}
		items = append(items, CleanupItem{
			ID: itemID(ItemReleaseAssets, r.R().ID, rel.Tag), Kind: ItemReleaseAssets, RepoID: r.R().ID, Key: r.Entry.Key,
			Label:  "Release " + rel.Tag + " (" + fmt.Sprint(len(rel.Stored)) + " Dateien)",
			Reason: fmt.Sprintf("älter als die neuesten %d Releases", ret.ReleasesKeep), Size: size,
			Since: timeOf(rel.Published), Path: rel.Dir,
		})
	}
	return items
}

// refCandidates proposes backups/snapshots older than keepFor. keepNewest
// protects the newest snapshot regardless of age.
func (l *Library) refCandidates(ctx context.Context, r *Repo, ns, kind string, keepFor time.Duration, now time.Time, keepNewest bool) []CleanupItem {
	if keepFor <= 0 {
		return nil
	}
	out, err := l.Git.Run(ctx, r.Paths.Mirror, nil, "for-each-ref", "--format=%(refname)", ns)
	if err != nil {
		return nil
	}
	stamps := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		rest, ok := strings.CutPrefix(line, ns)
		if !ok {
			continue
		}
		stamp, _, _ := strings.Cut(rest, "/")
		stamps[stamp] = true
	}
	var sorted []string
	for s := range stamps {
		sorted = append(sorted, s)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	var items []CleanupItem
	label := map[string]string{ItemBackup: "Force-Push-Sicherung", ItemSnapshot: "Snapshot"}[kind]
	for i, s := range sorted {
		t, err := time.Parse("20060102T150405Z", s)
		if err != nil || now.Sub(t) < keepFor || (keepNewest && i == 0) {
			continue
		}
		items = append(items, CleanupItem{
			ID: itemID(kind, r.R().ID, s), Kind: kind, RepoID: r.R().ID, Key: r.Entry.Key,
			Label:  label + " vom " + t.Local().Format("02.01.2006 15:04"),
			Reason: "älter als " + repometa.Duration(keepFor).String(), Since: t, Ref: ns + s + "/",
		})
	}
	return items
}

// MarkForDeletion proposes (or withdraws) a whole repository for the
// cleanup dialog. Repositories are never removed by rules alone.
func (l *Library) MarkForDeletion(ctx context.Context, ref string, mark bool, actor string) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	st := l.readCleanupState()
	id := r.R().ID
	has := slices.Contains(st.Marked, id)
	switch {
	case mark && !has:
		st.Marked = append(st.Marked, id)
	case !mark && has:
		st.Marked = slices.DeleteFunc(st.Marked, func(s string) bool { return s == id })
	}
	if err := l.writeCleanupState(st); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: map[bool]string{true: "mark_delete", false: "unmark_delete"}[mark], RepoID: id})
	return nil
}

// IsMarked reports whether a repository is marked for deletion.
func (l *Library) IsMarked(id string) bool {
	return slices.Contains(l.readCleanupState().Marked, id)
}

// KeepItems stops proposing items ("Anheften/Behalten" for things that are
// not versions). Versions are pinned in repo.yaml instead.
func (l *Library) KeepItems(ctx context.Context, ids []string, actor string) error {
	plan, err := l.CleanupPlan(ctx)
	if err != nil {
		return err
	}
	st := l.readCleanupState()
	for _, it := range plan {
		if !slices.Contains(ids, it.ID) {
			continue
		}
		if it.Kind == ItemReleaseAssets {
			tag := strings.TrimPrefix(strings.SplitN(it.Label, " (", 2)[0], "Release ")
			if rel, err := readRelease(it.Path); err == nil {
				tag = rel.Tag
			}
			if _, err := l.Pin(ctx, it.RepoID, tag, true, actor); err != nil {
				return err
			}
			continue
		}
		if it.Kind == ItemRepo {
			st.Marked = slices.DeleteFunc(st.Marked, func(s string) bool { return s == it.RepoID })
			continue
		}
		st.Kept = append(st.Kept, it.ID)
	}
	return l.writeCleanupState(st)
}

// ---- Trash (T-3.4) ----

// TrashEntry is one item in the trash, described by trash/<id>/manifest.yaml.
type TrashEntry struct {
	ID        string    `yaml:"id"`
	Kind      string    `yaml:"kind"`
	RepoID    string    `yaml:"repo_id"`
	Key       string    `yaml:"key"`
	Label     string    `yaml:"label"`
	Reason    string    `yaml:"reason"`
	Size      int64     `yaml:"size"`
	TrashedAt time.Time `yaml:"trashed_at"`
	By        string    `yaml:"by"`
	// Moved maps paths in the payload folder to their original place
	// (both relative to the data root, slash-separated).
	Moved map[string]string `yaml:"moved"`
	// Mirror and Ref describe moved refs: original namespace → trash
	// namespace inside the bare repository.
	Mirror   string `yaml:"mirror,omitempty"`
	Ref      string `yaml:"ref,omitempty"`
	TrashRef string `yaml:"trash_ref,omitempty"`
	// Due is when the entry may be deleted for good (after confirmation).
	Due time.Time `yaml:"-"`
}

const (
	trashManifest = "manifest.yaml"
	trashPayload  = "payload"
	// TrashRefNamespace keeps trashed refs inside the repository.
	TrashRefNamespace = "refs/reposhelf/trash/"
)

// MoveToTrash moves the given plan items into the trash. Nothing is deleted.
func (l *Library) MoveToTrash(ctx context.Context, ids []string, actor string) (int, error) {
	plan, err := l.CleanupPlan(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range plan {
		if !slices.Contains(ids, it.ID) {
			continue
		}
		if err := l.trashItem(ctx, it, actorOr(actor)); err != nil {
			return n, fmt.Errorf("%s: %w", it.Label, err)
		}
		n++
	}
	l.events.publish(Event{Kind: "cleanup"})
	return n, nil
}

func (l *Library) trashItem(ctx context.Context, it CleanupItem, actor string) error {
	now := l.now().UTC()
	te := TrashEntry{
		ID: now.Format("20060102T150405Z") + "-" + it.ID, Kind: it.Kind, RepoID: it.RepoID, Key: it.Key,
		Label: it.Label, Reason: it.Reason, Size: it.Size, TrashedAt: now, By: actor, Moved: map[string]string{},
	}
	dir := filepath.Join(l.Layout.TrashDir(), te.ID)
	payload := filepath.Join(dir, trashPayload)
	if err := os.MkdirAll(payload, dirPerm); err != nil {
		return err
	}
	move := func(abs string) error {
		if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		rel, err := filepath.Rel(l.Layout.Root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("refusing to move %s: outside the data folder", abs)
		}
		dst := filepath.Join(payload, rel)
		if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
			return err
		}
		if err := os.Rename(abs, dst); err != nil {
			return err
		}
		te.Moved[filepath.ToSlash(rel)] = filepath.ToSlash(rel)
		return nil
	}

	switch it.Kind {
	case ItemReleaseAssets:
		// Keep release.json and notes; move only the files.
		rel, err := readRelease(it.Path)
		if err != nil {
			return err
		}
		for _, a := range rel.Stored {
			if a.SameAs == "" {
				if err := move(filepath.Join(it.Path, a.File)); err != nil {
					return err
				}
			}
		}
		rel.Stored = nil
		if err := writeRelease(it.Path, rel); err != nil {
			return err
		}
	case ItemBackup, ItemSnapshot:
		r, err := l.Get(ctx, it.RepoID)
		if err != nil {
			return err
		}
		te.Mirror, _ = filepath.Rel(l.Layout.Root, r.Paths.Mirror)
		te.Ref = it.Ref
		te.TrashRef = TrashRefNamespace + te.ID + "/"
		if _, err := l.Git.MoveRefs(ctx, r.Paths.Mirror, it.Ref, te.TrashRef); err != nil {
			return err
		}
	case ItemCapsule:
		if err := move(it.Path); err != nil {
			return err
		}
		if err := move(strings.TrimSuffix(it.Path, ".yaml") + ".files.json"); err != nil {
			return err
		}
	case ItemBuild, ItemActions:
		if err := move(it.Path); err != nil {
			return err
		}
	case ItemBlobs:
		for _, p := range strings.Split(it.Path, "\n") {
			if err := move(p); err != nil {
				return err
			}
		}
	case ItemRepo:
		r, err := l.Get(ctx, it.RepoID)
		if err != nil {
			return err
		}
		unlock := l.locks.lock(r.R().ID)
		defer unlock()
		actions, _ := l.actionsDir(r)
		for _, p := range []string{r.Paths.Meta, r.Paths.Mirror, l.wikiDir(r), r.Paths.Releases, actions} {
			if err := move(p); err != nil {
				return err
			}
		}
		if err := l.Store.Delete(ctx, r.R().ID); err != nil {
			return err
		}
		st := l.readCleanupState()
		st.Marked = slices.DeleteFunc(st.Marked, func(s string) bool { return s == r.R().ID })
		if err := l.writeCleanupState(st); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown item kind %q", it.Kind)
	}
	if err := l.writeTrashEntry(dir, te); err != nil {
		return err
	}
	l.Record(Activity{Actor: actor, Action: "trash", RepoID: it.RepoID, Detail: it.Label})
	if it.Kind != ItemRepo && it.RepoID != "" {
		if r, err := l.Get(ctx, it.RepoID); err == nil {
			unlock := l.locks.lock(r.R().ID)
			l.refreshStats(ctx, r)
			unlock()
		}
	}
	return nil
}

func (l *Library) writeTrashEntry(dir string, te TrashEntry) error {
	b, err := repometa.NewFile(te).Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, trashManifest), b, filePerm)
}

// Trash lists the trash, newest first.
func (l *Library) Trash() ([]TrashEntry, error) {
	entries, err := os.ReadDir(l.Layout.TrashDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	days := l.Config().Retention.TrashDays
	var out []TrashEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(l.Layout.TrashDir(), e.Name(), trashManifest))
		if err != nil {
			continue
		}
		f, err := repometa.ParseFile(raw, TrashEntry{})
		if err != nil || f.Value.ID != e.Name() {
			continue
		}
		te := f.Value
		te.Due = te.TrashedAt.AddDate(0, 0, days)
		out = append(out, te)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TrashedAt.After(out[j].TrashedAt) })
	return out, nil
}

// Restore moves a trash entry back to where it came from. It refuses when
// the original place is occupied again.
func (l *Library) Restore(ctx context.Context, id, actor string) error {
	te, dir, err := l.trashEntry(id)
	if err != nil {
		return err
	}
	for from, to := range te.Moved {
		if _, err := os.Stat(filepath.Join(l.Layout.Root, filepath.FromSlash(to))); err == nil {
			return fmt.Errorf("%w: %s exists again", ErrExists, to)
		}
		_ = from
	}
	for from, to := range te.Moved {
		src := filepath.Join(dir, trashPayload, filepath.FromSlash(from))
		dst := filepath.Join(l.Layout.Root, filepath.FromSlash(to))
		if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	if te.TrashRef != "" {
		mirror := filepath.Join(l.Layout.Root, te.Mirror)
		if _, err := l.Git.MoveRefs(ctx, mirror, te.TrashRef, te.Ref); err != nil {
			return err
		}
	}
	if te.Kind == ItemReleaseAssets {
		l.restoreReleaseManifest(te)
	}
	if err := os.RemoveAll(dir); err != nil { // only our manifest and empty payload remain
		return err
	}
	if te.Kind == ItemRepo {
		if _, err := l.Reindex(ctx); err != nil {
			return err
		}
	} else if r, err := l.Get(ctx, te.RepoID); err == nil {
		unlock := l.locks.lock(r.R().ID)
		l.refreshStats(ctx, r)
		unlock()
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "restore", RepoID: te.RepoID, Detail: te.Label})
	l.events.publish(Event{Kind: "cleanup"})
	return nil
}

// restoreReleaseManifest re-registers restored asset files.
func (l *Library) restoreReleaseManifest(te TrashEntry) {
	byDir := map[string][]string{}
	for _, to := range te.Moved {
		abs := filepath.Join(l.Layout.Root, filepath.FromSlash(to))
		byDir[filepath.Dir(abs)] = append(byDir[filepath.Dir(abs)], filepath.Base(abs))
	}
	for dir, files := range byDir {
		rel, err := readRelease(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f
			var size int64
			for _, a := range rel.Assets {
				if encodeName(a.Name) == f {
					name, size = a.Name, a.Size
				}
			}
			if info, err := os.Stat(filepath.Join(dir, f)); err == nil {
				size = info.Size()
			}
			rel.Stored = append(rel.Stored, StoredAsset{Name: name, File: f, Size: size, SHA256: fileSHA(filepath.Join(dir, f))})
		}
		if err := writeRelease(dir, rel); err != nil {
			l.Log.Warn("restore release manifest", "dir", dir, "err", err)
		}
	}
}

func fileSHA(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := ioCopy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Purge deletes trash entries for good. The caller must have asked the user
// (the web UI shows a confirmation; MCP cannot reach this, D-011).
func (l *Library) Purge(ctx context.Context, ids []string, actor string) (int, error) {
	n := 0
	for _, id := range ids {
		te, dir, err := l.trashEntry(id)
		if err != nil {
			return n, err
		}
		if te.TrashRef != "" {
			mirror := filepath.Join(l.Layout.Root, te.Mirror)
			if _, err := os.Stat(mirror); err == nil {
				if err := l.Git.DeleteRefs(ctx, mirror, te.TrashRef); err != nil {
					return n, err
				}
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return n, err
		}
		l.Record(Activity{Actor: actorOr(actor), Action: "purge", RepoID: te.RepoID, Detail: te.Label})
		n++
	}
	l.events.publish(Event{Kind: "cleanup"})
	return n, nil
}

func (l *Library) trashEntry(id string) (TrashEntry, string, error) {
	list, err := l.Trash()
	if err != nil {
		return TrashEntry{}, "", err
	}
	for _, te := range list {
		if te.ID == id {
			return te, filepath.Join(l.Layout.TrashDir(), te.ID), nil
		}
	}
	return TrashEntry{}, "", fmt.Errorf("trash entry %s: %w", id, ErrNotFound)
}

// StartupReview is what the cleanup dialog shows at start (T-3.3).
type StartupReview struct {
	Pending []CleanupItem // proposed by rules, waiting for a decision
	Due     []TrashEntry  // in the trash longer than trash_days
}

// Review returns pending decisions; empty when there is nothing to ask.
func (l *Library) Review(ctx context.Context) (StartupReview, error) {
	var rv StartupReview
	var err error
	if rv.Pending, err = l.CleanupPlan(ctx); err != nil {
		return rv, err
	}
	trash, err := l.Trash()
	if err != nil {
		return rv, err
	}
	now := l.now()
	for _, te := range trash {
		if now.After(te.Due) {
			rv.Due = append(rv.Due, te)
		}
	}
	return rv, nil
}
