// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	tests := []struct {
		in        string
		want      time.Duration
		canonical string
	}{
		{"30d", 30 * day, "30d"},
		{"6h", 6 * time.Hour, "6h"},
		{"1d12h", 36 * time.Hour, "1d12h"},
		{"36h", 36 * time.Hour, "1d12h"},
		{"90m", 90 * time.Minute, "1h30m"},
		{"45s", 45 * time.Second, "45s"},
		{"0s", 0, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDuration(tt.in)
			if err != nil {
				t.Fatalf("ParseDuration(%q) error = %v", tt.in, err)
			}
			if time.Duration(got) != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.in, time.Duration(got), tt.want)
			}
			if got.String() != tt.canonical {
				t.Errorf("String() = %q, want %q", got.String(), tt.canonical)
			}
		})
	}
}

func TestDurationRejects(t *testing.T) {
	for _, in := range []string{"", "6", "1.5h", "-1h", "6H", "1h1d", "1w", " 6h", "99999999999999999999d", "200000d", "106751d24h"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) error = nil", in)
		}
	}
}

func TestNewDate(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	d := NewDate(time.Date(2026, 11, 9, 0, 30, 0, 0, berlin)) // 23:30 UTC on the 8th
	if d.String() != "2026-11-08" {
		t.Errorf("NewDate() = %s, want 2026-11-08 (UTC day)", d)
	}
}
