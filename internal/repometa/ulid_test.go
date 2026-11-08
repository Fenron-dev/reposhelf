// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"testing"
	"time"
)

func TestNewID(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	a, b := NewID(t0), NewID(t0.Add(time.Millisecond))
	if !isULID(a) || !isULID(b) {
		t.Fatalf("invalid ULIDs %s %s", a, b)
	}
	if a[:10] >= b[:10] {
		t.Errorf("IDs do not sort by time: %s %s", a, b)
	}
	if NewID(t0) == NewID(t0) {
		t.Error("IDs not random")
	}
	// Known timestamp encoding: 1469918176385 ms → 01ARYZ6S41 (ULID spec).
	if got := NewID(time.UnixMilli(1469918176385))[:10]; got != "01ARYZ6S41" {
		t.Errorf("timestamp part = %s", got)
	}
	if got := NewID(time.UnixMilli(1<<48 - 1))[:10]; got != "7ZZZZZZZZZ" {
		t.Errorf("max timestamp = %s", got)
	}
}
