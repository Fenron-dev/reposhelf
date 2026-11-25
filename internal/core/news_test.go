// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestNews: a sync records new versions and commits on the default branch;
// News sums them up since a time (V-3).
func TestNews(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("o", "tool")
	f.NewRepo("o", "quiet")
	l, clk := openLib(t, f)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "tool"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	q, err := l.Add(ctx, AddRequest{URL: f.URL("o", "quiet"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	clk.Add(time.Hour)
	since := clk.Now()
	clk.Add(time.Minute)

	up.Commit("a.txt", "a", "a")
	up.Commit("b.txt", "b", "b")
	up.Git("tag", "v1.1")
	up.Commit("c.txt", "c", "c")
	up.Git("tag", "v1.2")
	up.Push()
	up.Git("push", "--quiet", "origin", "--tags")
	if _, err := l.Sync(ctx, r.R().ID, "t"); err != nil {
		t.Fatal(err)
	}
	l.Sync(ctx, q.R().ID, "t") // nothing new

	n, err := l.News(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Items) != 1 {
		t.Fatalf("items %+v", n.Items)
	}
	it := n.Items[0]
	if it.Key != r.Entry.Key || it.Commits != 3 || !slices.Equal(it.Tags, []string{"v1.1", "v1.2"}) || it.Added || n.Versions != 1 {
		t.Errorf("item %+v versions %d", it, n.Versions)
	}
	// Before the adds, both repositories are new.
	all, _ := l.News(ctx, since.Add(-2*time.Hour))
	if all.Added != 2 {
		t.Errorf("added %d, want 2", all.Added)
	}
	// After the sync nothing is new.
	clk.Add(time.Minute)
	if later, _ := l.News(ctx, clk.Now()); !later.Empty() {
		t.Errorf("news after the visit: %+v", later)
	}
}
