// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- What is new (V-3, T-9.30, D-036) ----
//
// Every sync records what arrived (SyncChanges in the activity log). The
// library sums that up since the user's last visit, together with problem
// cases and the storage situation.

// SyncChanges is what one sync brought in.
type SyncChanges struct {
	Tags      []string `json:"tags,omitempty"`      // new versions
	Commits   int      `json:"commits,omitempty"`   // new commits on the default branch
	Files     int      `json:"files,omitempty"`     // release files downloaded
	Bytes     int64    `json:"bytes,omitempty"`     // their size
	Rewritten int      `json:"rewritten,omitempty"` // force-pushed refs (backed up)
}

func (c SyncChanges) empty() bool {
	return len(c.Tags) == 0 && c.Commits == 0 && c.Files == 0 && c.Rewritten == 0
}

// syncChanges reads the changes out of a sync report; nil if nothing came.
func (l *Library) syncChanges(ctx context.Context, r *Repo, rep SyncReport) *SyncChanges {
	c := SyncChanges{Files: rep.Releases.Downloaded, Bytes: rep.Releases.Bytes, Rewritten: len(rep.Mirror.Rewritten)}
	for _, ch := range rep.Mirror.New {
		if t, ok := strings.CutPrefix(ch.Ref, "refs/tags/"); ok {
			c.Tags = append(c.Tags, t)
		}
	}
	head := "refs/heads/" + l.Git.Head(ctx, r.Paths.Mirror)
	for _, ch := range rep.Mirror.Updated {
		if ch.Ref == head {
			if n, err := l.Git.CountCommits(ctx, r.Paths.Mirror, ch.Old, ch.New); err == nil {
				c.Commits = n
			}
		}
	}
	if c.empty() {
		return nil
	}
	return &c
}

// maxNewsActivity bounds how far back the activity log is read.
const maxNewsActivity = 5000

// NewsItem is what arrived in one repository.
type NewsItem struct {
	RepoID, Key string
	Last        time.Time
	Added       bool // new in the library
	SyncChanges
}

// News is the "Was ist neu" block.
type News struct {
	Since    time.Time
	Items    []NewsItem // newest first
	Versions int        // repositories with new versions
	Added    int
	// Problems are repositories whose original is in trouble or whose sync fails.
	Problems []store.Entry
}

// Empty reports whether there is nothing to show.
func (n News) Empty() bool { return len(n.Items) == 0 && len(n.Problems) == 0 }

// News sums up what happened since since.
func (l *Library) News(ctx context.Context, since time.Time) (News, error) {
	n := News{Since: since}
	byRepo := map[string]*NewsItem{}
	for _, a := range l.RecentActivity(maxNewsActivity, "") {
		if a.Time.Before(since) {
			break // newest first
		}
		if a.RepoID == "" {
			continue
		}
		added := a.Action == "add" || a.Action == "create" || a.Action == "import-folder" || a.Action == "import_zip" || a.Action == "adopt"
		if !added && (a.Action != "sync" || a.Changes == nil) {
			continue
		}
		it := byRepo[a.RepoID]
		if it == nil {
			it = &NewsItem{RepoID: a.RepoID, Last: a.Time}
			byRepo[a.RepoID] = it
		}
		if added {
			it.Added = true
			continue
		}
		c := a.Changes
		// Activity runs newest first; keep tags in release order.
		it.Tags = append(slices.Clone(c.Tags), it.Tags...)
		it.Commits += c.Commits
		it.Files += c.Files
		it.Bytes += c.Bytes
		it.Rewritten += c.Rewritten
	}
	all, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return n, err
	}
	keys := map[string]string{}
	for _, e := range all {
		keys[e.ID] = e.Key
		if e.Status == StatusDegraded || e.Status == StatusGone {
			n.Problems = append(n.Problems, e)
		}
	}
	for id, it := range byRepo {
		key, ok := keys[id]
		if !ok {
			continue // removed since
		}
		it.Key = key
		if len(it.Tags) > 0 {
			n.Versions++
		}
		if it.Added {
			n.Added++
		}
		n.Items = append(n.Items, *it)
	}
	sort.Slice(n.Items, func(i, j int) bool { return n.Items[i].Last.After(n.Items[j].Last) })
	return n, nil
}
