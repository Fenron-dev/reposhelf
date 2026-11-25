// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func TestGrowth(t *testing.T) {
	total := func(s UsageSample) (int64, bool) { return s.Total, true }
	day := func(d int) string {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format(usageDate)
	}
	var lin []UsageSample
	for d := 0; d <= 20; d += 2 {
		lin = append(lin, UsageSample{Date: day(d), Total: 1000 + int64(d)*50})
	}
	g, ok := growth(lin, total)
	if !ok || math.Abs(g-50) > 1e-6 {
		t.Errorf("growth = %v %v, want 50", g, ok)
	}
	if _, ok := growth(lin[:3], total); ok {
		t.Error("four days gave a forecast")
	}
	// Samples older than the window do not count.
	old := append([]UsageSample{{Date: day(-200), Total: 0}}, lin...)
	if g, _ := growth(old, total); math.Abs(g-50) > 1e-6 {
		t.Errorf("old sample counted: %v", g)
	}
}

// TestStorageLimit: a collection over its limit is reported once, the
// samples are kept per day, and the report forecasts the free space (V-14).
func TestStorageLimit(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	f.NewRepo("o", "big")
	l, clk := openLib(t, f)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "big"), Wait: true, Collections: []string{"spiele"}})
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(1)
	if err := l.SaveCollection(Collection{ID: "spiele", Name: "Spiele", MaxSizeMB: &limit}, "t"); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countKind(l, NotifyStorage); n != 0 {
		t.Fatalf("%d warnings below the limit", n)
	}
	os.MkdirAll(r.Paths.Releases, 0o755)
	if err := os.WriteFile(filepath.Join(r.Paths.Releases, "big.bin"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Sync(ctx, r.R().ID, "t"); err != nil {
		t.Fatal(err)
	}
	clk.Add(time.Hour)
	l.RecordUsage(ctx)
	clk.Add(time.Hour)
	l.RecordUsage(ctx)
	if n := countKind(l, NotifyStorage); n != 1 {
		t.Fatalf("%d warnings over the limit, want 1", n)
	}
	if got := l.Notifications()[0].Title; !strings.Contains(got, "Spiele") {
		t.Errorf("title %q", got)
	}
	if hist := l.readUsage(); len(hist) != 1 {
		t.Errorf("%d samples on one day", len(hist))
	}
	rep, err := l.StorageReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Collections) != 1 || !rep.Collections[0].Over() || rep.Collections[0].Percent() < 200 {
		t.Errorf("collections %+v", rep.Collections)
	}
	if rep.Forecast {
		t.Error("forecast from one day")
	}
}

// TestCollectionWithoutLimit: collection files written before V-14 load
// without a limit and keep their other fields.
func TestCollectionWithoutLimit(t *testing.T) {
	f := testutil.NewForge(t)
	l, _ := openLib(t, f)
	old := "name: Alt\ndescription: vor V-14\ncolor: '#123456'\nretention:\n  releases_keep: 3\neigenes_feld: bleibt\n"
	p := filepath.Join(l.Layout.Path(layout.DirCollections), "alt.yaml")
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := l.Collection(context.Background(), "alt")
	if err != nil || c.MaxSizeMB != nil || c.Name != "Alt" || *c.Retention.ReleasesKeep != 3 {
		t.Fatalf("%+v %v", c, err)
	}
	limit := int64(500)
	c.MaxSizeMB = &limit
	if err := l.SaveCollection(c, "t"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "max_size_mb: 500") || !strings.Contains(string(b), "eigenes_feld: bleibt") {
		t.Errorf("saved:\n%s", b)
	}
	bad := int64(0)
	c.MaxSizeMB = &bad
	if err := l.SaveCollection(c, "t"); err == nil {
		t.Error("limit 0 accepted")
	}
}

func countKind(l *Library, kind string) int {
	n := 0
	for _, m := range l.Notifications() {
		if m.Kind == kind {
			n++
		}
	}
	return n
}
