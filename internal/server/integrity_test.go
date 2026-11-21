// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIntegrityPages: the check starts from the settings; damage shows in
// the library (V-1).
func TestIntegrityPages(t *testing.T) {
	e := setup(t)
	if body := e.get("/settings", 200); !strings.Contains(body, "Noch nie geprüft") {
		t.Error("settings without integrity section")
	}
	e.post("/settings/integrity", nil, 303)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, ok := e.lib.LastIntegrity(); ok && !e.lib.IntegrityRunning() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("check did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if body := e.get("/settings", 200); !strings.Contains(body, "alles in Ordnung") {
		t.Error("clean report not shown")
	}
	rels, _ := e.lib.Releases(context.Background(), e.repo.R().ID)
	p := filepath.Join(rels[0].Dir, rels[0].Stored[0].File)
	os.Chmod(p, 0o644)
	os.WriteFile(p, []byte("bitfäule"), 0o644)
	if _, err := e.lib.CheckIntegrity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if body := e.get("/", 200); !strings.Contains(body, "beschädigte oder fehlende") {
		t.Error("damage not shown in the library")
	}
	if body := e.get("/settings", 200); !strings.Contains(body, "Prüfsumme stimmt nicht") {
		t.Error("problem not listed")
	}
}
