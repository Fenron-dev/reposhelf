// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/store"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestSubmodulesStayBounded: only submodules on the same host are taken
// over, one level deep; others stay a relation (R-016).
func TestSubmodulesStayBounded(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	app := f.NewRepo("o", "app")
	app.Commit(".gitmodules", "[submodule \"lib\"]\n\tpath = lib\n\turl = "+f.URL("o", "lib")+"\n"+
		"[submodule \"fremd\"]\n\tpath = fremd\n\turl = https://other.example/x/fremd.git\n"+
		"[submodule \"lan\"]\n\tpath = lan\n\turl = http://192.168.1.10/nas/geheim.git\n", "submodules")
	app.Push()
	lib := f.NewRepo("o", "lib")
	lib.Commit(".gitmodules", "[submodule \"deep\"]\n\tpath = deep\n\turl = "+f.URL("o", "deep")+"\n", "deep")
	lib.Push()
	f.NewRepo("o", "deep")

	l, _ := openLib(t, f)
	if _, err := l.Add(ctx, AddRequest{URL: f.URL("o", "app"), Wait: true}); err != nil {
		t.Fatal(err)
	}
	all, _ := l.Store.List(ctx, storeAll())
	keys := map[string]bool{}
	for _, e := range all {
		keys[e.Key] = true
	}
	if !keys[hostName(f.Host)+"/o/lib"] {
		t.Errorf("same-host submodule not taken over: %v", keys)
	}
	if keys["other.example/x/fremd"] || keys["192.168.1.10/nas/geheim"] || len(keys) != 2 {
		t.Errorf("foreign submodules taken over: %v", keys)
	}
	// The submodule's own submodules are not followed.
	if _, err := l.Sync(ctx, hostName(f.Host)+"/o/lib", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(ctx, hostName(f.Host)+"/o/deep"); err == nil {
		t.Error("second level taken over")
	}
	r, _ := l.Get(ctx, hostName(f.Host)+"/o/app")
	rels := 0
	for _, rel := range r.R().Relations {
		if rel.Type == "depends_on" {
			rels++
		}
	}
	if rels != 3 {
		t.Errorf("relations = %+v", r.R().Relations)
	}
}

func storeAll() store.Query { return store.Query{} }
