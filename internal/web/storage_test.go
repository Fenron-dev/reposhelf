// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

func TestUsageChart(t *testing.T) {
	if newUsageChart([]core.UsageSample{{Date: "2026-11-01", Total: 5}}) != nil {
		t.Error("chart from one day")
	}
	c := newUsageChart([]core.UsageSample{
		{Date: "2026-11-01", Total: 400 << 20, Free: -1},
		{Date: "2026-11-11", Total: 800 << 20, Free: 10 << 30},
	})
	if c == nil || len(c.Points) != 2 || len(c.Ticks) != chartTicks {
		t.Fatalf("%+v", c)
	}
	// First point at the left edge, last at the right, larger value higher.
	if c.Points[0].X != chartLeft || c.Points[1].X != chartW-chartRight || c.Points[1].Y >= c.Points[0].Y {
		t.Errorf("points %+v", c.Points)
	}
	if !strings.Contains(c.Points[1].Label, "11.11.2026") || !strings.Contains(c.Points[1].Label, "frei 10,0 GB") || strings.Contains(c.Points[0].Label, "frei") {
		t.Errorf("labels %q / %q", c.Points[0].Label, c.Points[1].Label)
	}
	if c.Ticks[2].Label != "1000,0 MB" || c.Ticks[1].Label != "500,0 MB" {
		t.Errorf("ticks %+v", c.Ticks)
	}
	if c.First != "01.11.2026" || c.End.Label != "800,0 MB" {
		t.Errorf("first %s end %s", c.First, c.End.Label)
	}
}

func TestNiceCeil(t *testing.T) {
	for in, want := range map[float64]float64{0: 1, 3: 5, 10: 10, 11: 20, 450: 500, 501: 1000, 1.9e9: 2e9} {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%g) = %g, want %g", in, got, want)
		}
	}
}

func TestLimitGB(t *testing.T) {
	for in, want := range map[string]int64{"1": 1024, "1,5": 1536, " 0.5 ": 512, "100": 102400} {
		got, err := parseLimitGB(in)
		if err != nil || got == nil || *got != want {
			t.Errorf("parseLimitGB(%q) = %v %v, want %d", in, got, err, want)
			continue
		}
		if back, _ := parseLimitGB(limitGB(got)); *back != want {
			t.Errorf("round trip of %q: %d", in, *back)
		}
	}
	if got, err := parseLimitGB(""); got != nil || err != nil {
		t.Error("empty is not 'no limit'")
	}
	for _, bad := range []string{"0", "-1", "abc", "Inf"} {
		if _, err := parseLimitGB(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
