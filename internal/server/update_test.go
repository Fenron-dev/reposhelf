// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/update"
)

// TestUpdateCard: a newer release shows in the sidebar and the settings;
// in Docker there is only the hint; the daily check can be switched off (V-15).
func TestUpdateCard(t *testing.T) {
	e := setup(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.9.9", "html_url": "https://example.test/v9.9.9", "assets": []any{}})
	}))
	defer api.Close()
	u := &update.Updater{Current: "0.7.1", Flavor: update.FlavorDocker, API: api.URL, Client: api.Client(),
		StatePath: filepath.Join(e.lib.Layout.Path(layout.DirInternal), update.StateFile)}
	srv, err := New(e.lib, Options{Updates: u})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	post := func(form url.Values) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, hs.URL+"/settings/update", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", hs.URL)
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("POST %v: %d", form, resp.StatusCode)
		}
	}
	get := func(p string) string {
		t.Helper()
		resp, err := http.Get(hs.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if body := get("/settings"); !strings.Contains(body, "Noch nie geprüft") || strings.Contains(body, "verfügbar</a>") {
		t.Fatal("card before the check")
	}
	post(url.Values{"action": {"check"}})
	body := get("/settings")
	for _, want := range []string{"RepoShelf v9.9.9 verfügbar", "Neue Version <span class=\"mono\">v9.9.9</span>", "docker pull"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}
	if strings.Contains(body, "Herunterladen und installieren") {
		t.Error("docker offers to install")
	}
	post(url.Values{})
	if e.lib.Config().Updates.Check {
		t.Error("daily check still on")
	}
	post(url.Values{"check": {"on"}})
	if !e.lib.Config().Updates.Check {
		t.Error("daily check not switched on")
	}
}
