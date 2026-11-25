// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// Storage page (V-14, T-9.29): size of the library over time, forecast and
// the collections against their limits.

// Geometry of the usage chart (SVG user units; it scales with the card).
const (
	chartW, chartH        = 640, 200
	chartLeft, chartRight = 72, 12
	chartTop, chartBottom = 10, 26
	chartTicks            = 3
	// chartHeadroom keeps the line off the top edge.
	chartHeadroom = 1.1
	daysPerMonth  = 30
	usageDate     = "2006-01-02"
	mbPerGB       = 1024
	bytesUnit     = 1024
)

type chartPoint struct {
	X, Y  float64
	Label string
}

type chartTick struct {
	Y     float64
	Label string
}

type usageChart struct {
	W, H, Left, Right, Top, Bottom int
	Line, Area                     string
	Points                         []chartPoint
	Ticks                          []chartTick
	First, Last                    string
	End                            chartPoint
}

type storageData struct {
	R           core.StorageReport
	Chart       *usageChart
	PerMonth    int64
	Collections []core.CollectionUsage
}

func (s *Server) storagePage(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Lib.StorageReport(r.Context())
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	d := storageData{R: rep, Chart: newUsageChart(rep.History), PerMonth: int64(rep.GrowthPerDay * daysPerMonth)}
	d.Collections = append(d.Collections, rep.Collections...)
	sort.SliceStable(d.Collections, func(i, j int) bool { return d.Collections[i].Bytes > d.Collections[j].Bytes })
	s.render(w, r, "storage", "Speicher", "storage", d)
}

// newUsageChart lays out the library size per day; nil below two days.
func newUsageChart(hist []core.UsageSample) *usageChart {
	type day struct {
		t time.Time
		s core.UsageSample
	}
	var days []day
	for _, h := range hist {
		if t, err := time.Parse(usageDate, h.Date); err == nil {
			days = append(days, day{t, h})
		}
	}
	if len(days) < 2 {
		return nil
	}
	c := &usageChart{W: chartW, H: chartH, Left: chartLeft, Right: chartRight, Top: chartTop, Bottom: chartBottom}
	first, last := days[0].t, days[len(days)-1].t
	span := math.Max(last.Sub(first).Hours(), 1)
	var top int64
	for _, d := range days {
		top = max(top, d.s.Total)
	}
	// Round the top in the unit the labels use (KB, MB, GB …), so the
	// ticks read 0 · 10 GB · 20 GB rather than 9,3 GB · 18,6 GB.
	unit := 1.0
	for float64(top)*chartHeadroom/unit >= bytesUnit {
		unit *= bytesUnit
	}
	ymax := niceCeil(float64(top)*chartHeadroom/unit) * unit
	plotW, plotH := float64(chartW-chartLeft-chartRight), float64(chartH-chartTop-chartBottom)
	x := func(t time.Time) float64 { return chartLeft + t.Sub(first).Hours()/span*plotW }
	y := func(v float64) float64 { return chartTop + plotH - v/ymax*plotH }
	var line strings.Builder
	for i, d := range days {
		p := chartPoint{X: round1(x(d.t)), Y: round1(y(float64(d.s.Total))), Label: d.t.Format("02.01.2006") + ": " + humanSize(d.s.Total)}
		if d.s.Free >= 0 {
			p.Label += " · frei " + humanSize(d.s.Free)
		}
		c.Points = append(c.Points, p)
		if i > 0 {
			line.WriteByte(' ')
		}
		fmt.Fprintf(&line, "%g,%g", p.X, p.Y)
	}
	c.Line = line.String()
	base := round1(y(0))
	c.Area = fmt.Sprintf("%g,%g %s %g,%g", c.Points[0].X, base, c.Line, c.Points[len(c.Points)-1].X, base)
	for i := range chartTicks {
		v := ymax * float64(i) / float64(chartTicks-1)
		c.Ticks = append(c.Ticks, chartTick{Y: round1(y(v)), Label: humanSize(int64(v))})
	}
	c.First, c.Last = first.Format("02.01.2006"), last.Format("02.01.2006")
	c.End = c.Points[len(c.Points)-1]
	c.End.Label = humanSize(days[len(days)-1].s.Total)
	return c
}

// niceCeil rounds up to 1, 2 or 5 times a power of ten (at least 1).
func niceCeil(v float64) float64 {
	if v <= 1 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// limitGB shows a limit in MB as GB for the form ("" without limit).
func limitGB(mb *int64) string {
	if mb == nil {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(float64(*mb)/mbPerGB, 'f', -1, 64), ".", ",", 1)
}

// parseLimitGB reads a limit in GB (decimal comma allowed); "" is none.
func parseLimitGB(v string) (*int64, error) {
	v = strings.TrimSpace(strings.Replace(v, ",", ".", 1))
	if v == "" {
		return nil, nil
	}
	gb, err := strconv.ParseFloat(v, 64)
	if err != nil || gb <= 0 || math.IsInf(gb, 0) {
		return nil, fmt.Errorf("%w: Obergrenze %q – eine Zahl größer als 0 in GB eingeben", core.ErrInvalid, v)
	}
	mb := int64(math.Ceil(gb * mbPerGB))
	return &mb, nil
}
