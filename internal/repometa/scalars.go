// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	day         = 24 * time.Hour
	dateForm    = "2006-01-02"
	maxDuration = time.Duration(1<<63 - 1)
)

// durationPattern accepts whole units in fixed order, e.g. 30d, 6h, 1d12h,
// 90m. Go's time.ParseDuration has no day unit and allows fractions, which
// makes hand-written values ambiguous.
var durationPattern = regexp.MustCompile(`^(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$`)

var errBadDuration = errors.New("duration must look like 30d, 6h, 1d12h or 90m")

// Duration is a span written with d/h/m/s units.
type Duration time.Duration

// ParseDuration parses the repo.yaml duration syntax.
func ParseDuration(s string) (Duration, error) {
	m := durationPattern.FindStringSubmatch(s)
	if s == "" || m == nil {
		return 0, fmt.Errorf("%q: %w", s, errBadDuration)
	}
	units := []time.Duration{day, time.Hour, time.Minute, time.Second}
	var total time.Duration
	for i, unit := range units {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil || n > int64((maxDuration-total)/unit) {
			return 0, fmt.Errorf("%q: value too large", s)
		}
		total += time.Duration(n) * unit
	}
	return Duration(total), nil
}

// String formats d in the canonical repo.yaml form (largest units first).
func (d Duration) String() string {
	if d == 0 {
		return "0s"
	}
	rest := time.Duration(d)
	var b strings.Builder
	for _, u := range []struct {
		unit   time.Duration
		suffix string
	}{{day, "d"}, {time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if n := rest / u.unit; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, u.suffix)
			rest -= n * u.unit
		}
	}
	return b.String()
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	parsed, err := ParseDuration(n.Value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Date is a calendar day without time of day, written as YYYY-MM-DD.
type Date struct{ time.Time }

// NewDate returns the calendar day of t in UTC.
func NewDate(t time.Time) Date {
	y, m, d := t.UTC().Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// String formats the date as YYYY-MM-DD.
func (d Date) String() string { return d.Format(dateForm) }

// MarshalYAML implements yaml.Marshaler. The value is written unquoted like a
// YAML date, as a hand editor would write it.
func (d Date) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: d.String()}, nil
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Date) UnmarshalYAML(n *yaml.Node) error {
	t, err := time.Parse(dateForm, n.Value)
	if err != nil {
		return fmt.Errorf("date %q must look like 2026-11-08", n.Value)
	}
	d.Time = t
	return nil
}
