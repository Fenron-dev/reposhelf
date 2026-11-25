// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/fsinfo"
	"github.com/Fenron-dev/reposhelf/internal/layout"
)

// ---- Storage planning (V-14, T-9.29, D-036) ----
//
// RepoShelf keeps one sample of its size per day (the library, every
// collection, the free space of the data volume). From the samples it
// tells how fast the library grows and how long the free space lasts.
// Collections can have a size limit; going over it only warns (D-036),
// syncs and downloads go on.

const (
	// UsageFile keeps the daily samples (below .reposhelf/).
	UsageFile = "usage.jsonl"
	// usageEvery: today's sample is refreshed and the limits checked that
	// often, so a large download is noticed the same day.
	usageEvery = time.Hour
	// maxUsageSamples: two years of daily samples.
	maxUsageSamples = 730
	// forecastWindow: growth is measured over this span of samples.
	forecastWindow = 90 * 24 * time.Hour
	// forecastMinSpan: shorter histories give no forecast (noise).
	forecastMinSpan = 7 * 24 * time.Hour
	// forecastWarnDays: warn when the free space lasts less than this.
	forecastWarnDays = 30
	usageDate        = "2006-01-02"
	bytesPerMB       = 1 << 20
	bytesPerGB       = 1 << 30
	hoursPerDay      = 24
)

// UsageSample is the size of the library on one day.
type UsageSample struct {
	Date        string           `json:"date"` // local date, YYYY-MM-DD
	Total       int64            `json:"total"`
	Free        int64            `json:"free"` // -1: unknown
	Collections map[string]int64 `json:"collections,omitempty"`
	// Over and Short remember what was already reported, so a warning is
	// sent once when a limit is crossed, not every hour.
	Over  []string `json:"over,omitempty"`
	Short bool     `json:"short,omitempty"`
}

// CollectionUsage is the size of one collection against its limit.
type CollectionUsage struct {
	ID, Name     string
	Bytes        int64
	Limit        int64 // 0: none
	GrowthPerDay float64
}

// Over reports whether the collection is above its limit.
func (c CollectionUsage) Over() bool { return c.Limit > 0 && c.Bytes > c.Limit }

// Percent of the limit used (0 without limit).
func (c CollectionUsage) Percent() int {
	if c.Limit <= 0 {
		return 0
	}
	return int(c.Bytes * 100 / c.Limit)
}

// StorageReport is what the storage page shows.
type StorageReport struct {
	Now          UsageSample
	History      []UsageSample // oldest first
	GrowthPerDay float64       // bytes per day; 0 without forecast
	Forecast     bool          // enough history for GrowthPerDay
	DaysLeft     int           // free space at this growth; -1: not running out
	Collections  []CollectionUsage
}

// Short reports whether the free space lasts less than forecastWarnDays.
func (r StorageReport) Short() bool {
	return r.Forecast && r.DaysLeft >= 0 && r.DaysLeft < forecastWarnDays
}

// WarnDays is the threshold of Short, for texts.
func (r StorageReport) WarnDays() int { return forecastWarnDays }

func (l *Library) usagePath() string { return l.Layout.Path(layout.DirInternal, UsageFile) }

// usageDue reports whether the scheduler should refresh today's sample.
func (l *Library) usageDue() bool {
	l.usageMu.Lock()
	defer l.usageMu.Unlock()
	return l.usageLast.IsZero() || l.now().Sub(l.usageLast) >= usageEvery
}

// StorageReport measures the library now and adds the stored history.
func (l *Library) StorageReport(ctx context.Context) (StorageReport, error) {
	now, cols, err := l.measureUsage(ctx)
	if err != nil {
		return StorageReport{}, err
	}
	l.usageMu.Lock()
	hist := l.readUsage()
	l.usageMu.Unlock()
	return l.report(now, cols, hist), nil
}

// RecordUsage stores today's sample and warns about crossed limits.
func (l *Library) RecordUsage(ctx context.Context) error {
	now, cols, err := l.measureUsage(ctx)
	if err != nil {
		return err
	}
	l.usageMu.Lock()
	defer l.usageMu.Unlock()
	l.usageLast = l.now()
	hist := l.readUsage()
	var prev UsageSample
	if n := len(hist); n > 0 {
		prev = hist[n-1]
		if prev.Date == now.Date {
			hist = hist[:n-1]
		}
	}
	rep := l.report(now, cols, hist)
	for _, c := range rep.Collections {
		if !c.Over() {
			continue
		}
		now.Over = append(now.Over, c.ID)
		if !slices.Contains(prev.Over, c.ID) {
			l.Notify(Notification{Kind: NotifyStorage, Title: "Sammlung „" + c.Name + "“ über der Obergrenze",
				Text: sizeText(c.Bytes) + " von " + sizeText(c.Limit) + ". RepoShelf gleicht weiter ab; unter „Speicher“ und „Aufräumen“ lässt sich Platz schaffen."})
		}
	}
	now.Short = rep.Short()
	if now.Short && !prev.Short {
		l.Notify(Notification{Kind: NotifyStorage, Title: "Speicherplatz wird knapp",
			Text: fmt.Sprintf("Bei der jetzigen Zunahme (%s pro Monat) reicht der freie Platz (%s) noch etwa %d Tage.",
				sizeText(int64(rep.GrowthPerDay*daysPerMonth)), sizeText(now.Free), rep.DaysLeft)})
	}
	hist = append(hist, now)
	if len(hist) > maxUsageSamples {
		hist = hist[len(hist)-maxUsageSamples:]
	}
	return l.writeUsage(hist)
}

// daysPerMonth turns growth per day into growth per month for texts.
const daysPerMonth = 30

// measureUsage takes a sample of now; cols carries names and limits.
func (l *Library) measureUsage(ctx context.Context) (UsageSample, []Collection, error) {
	t, err := l.Store.Totals(ctx)
	if err != nil {
		return UsageSample{}, nil, fmt.Errorf("storage totals: %w", err)
	}
	s := UsageSample{Date: l.now().Local().Format(usageDate), Total: t.SizeBytes, Free: fsinfo.FreeBytes(l.Layout.Root)}
	cols, err := l.Collections(ctx)
	if err != nil {
		return UsageSample{}, nil, fmt.Errorf("storage collections: %w", err)
	}
	for _, c := range cols {
		members, err := l.CollectionMembers(ctx, c.ID)
		if err != nil {
			continue
		}
		var sum int64
		for _, e := range members {
			sum += e.SizeBytes
		}
		if s.Collections == nil {
			s.Collections = map[string]int64{}
		}
		s.Collections[c.ID] = sum
	}
	return s, cols, nil
}

func (l *Library) report(now UsageSample, cols []Collection, hist []UsageSample) StorageReport {
	all := append(slices.Clone(hist), now)
	if n := len(hist); n > 0 && hist[n-1].Date == now.Date {
		all = append(slices.Clone(hist[:n-1]), now)
	}
	r := StorageReport{Now: now, History: all, DaysLeft: -1}
	r.GrowthPerDay, r.Forecast = growth(all, func(s UsageSample) (int64, bool) { return s.Total, true })
	if r.Forecast && r.GrowthPerDay > 0 && now.Free >= 0 {
		r.DaysLeft = int(float64(now.Free) / r.GrowthPerDay)
	}
	for _, c := range cols {
		u := CollectionUsage{ID: c.ID, Name: c.Name, Bytes: now.Collections[c.ID]}
		if c.MaxSizeMB != nil {
			u.Limit = *c.MaxSizeMB * bytesPerMB
		}
		u.GrowthPerDay, _ = growth(all, func(s UsageSample) (int64, bool) {
			v, ok := s.Collections[c.ID]
			return v, ok
		})
		r.Collections = append(r.Collections, u)
	}
	return r
}

// growth is the least-squares slope in bytes per day over the samples of
// the last forecastWindow; ok is false for too short a history.
func growth(samples []UsageSample, value func(UsageSample) (int64, bool)) (float64, bool) {
	type pt struct{ x, y float64 }
	var pts []pt
	var last time.Time
	if n := len(samples); n > 0 {
		last, _ = time.Parse(usageDate, samples[n-1].Date)
	}
	for _, s := range samples {
		d, err := time.Parse(usageDate, s.Date)
		v, ok := value(s)
		if err != nil || !ok || last.Sub(d) > forecastWindow {
			continue
		}
		pts = append(pts, pt{d.Sub(last).Hours() / hoursPerDay, float64(v)})
	}
	if len(pts) < 2 || (pts[len(pts)-1].x-pts[0].x)*hoursPerDay < forecastMinSpan.Hours() {
		return 0, false
	}
	var sx, sy, sxx, sxy float64
	for _, p := range pts {
		sx, sy, sxx, sxy = sx+p.x, sy+p.y, sxx+p.x*p.x, sxy+p.x*p.y
	}
	n := float64(len(pts))
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, false
	}
	return (n*sxy - sx*sy) / den, true
}

func (l *Library) readUsage() []UsageSample {
	f, err := os.Open(l.usagePath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []UsageSample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var s UsageSample
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.Date != "" {
			out = append(out, s)
		}
	}
	return out
}

func (l *Library) writeUsage(all []UsageSample) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, s := range all {
		if err := enc.Encode(s); err != nil {
			return err
		}
	}
	if err := atomicfile.WriteFile(l.usagePath(), b.Bytes(), filePerm); err != nil {
		return fmt.Errorf("write %s: %w", UsageFile, err)
	}
	return nil
}

// sizeText formats bytes for messages (German decimal comma).
func sizeText(b int64) string {
	switch {
	case b >= bytesPerGB:
		return strings.Replace(strconv.FormatFloat(math.Round(float64(b)/bytesPerGB*10)/10, 'f', 1, 64), ".", ",", 1) + " GB"
	case b >= bytesPerMB:
		return strconv.FormatInt(b/bytesPerMB, 10) + " MB"
	}
	return strconv.FormatInt(b/1024, 10) + " KB"
}
