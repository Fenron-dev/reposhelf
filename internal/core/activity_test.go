// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"os"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestActivityRotates: the log stays bounded; recent entries survive the
// rotation (R-026).
func TestActivityRotates(t *testing.T) {
	l, _ := openLib(t, testutil.NewForge(t))
	big := strings.Repeat("x", 4096)
	for i := 0; i < (maxActivityBytes/4096)*2+50; i++ {
		l.Record(Activity{Actor: "test", Action: "fill", Detail: big})
	}
	l.Record(Activity{Actor: "test", Action: "last"})
	st, err := os.Stat(l.activity.path)
	if err != nil || st.Size() > maxActivityBytes+8192 {
		t.Errorf("log size = %v, %v", st, err)
	}
	if _, err := os.Stat(l.activity.older(1)); err != nil {
		t.Error("no rotated file")
	}
	if _, err := os.Stat(l.activity.older(3)); err == nil {
		t.Error("more than two old files")
	}
	if got := l.RecentActivity(3, ""); len(got) != 3 || got[0].Action != "last" {
		t.Errorf("recent = %+v", got)
	}
}
