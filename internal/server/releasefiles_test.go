// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/config"
)

// postHX sends a form like htmx does (with the triggering field's name).
func (e *env) postHX(path, trigger string, form url.Values) string {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.http.URL)
	req.Header.Set("HX-Request", "true")
	if trigger != "" {
		req.Header.Set("HX-Trigger-Name", trigger)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		e.t.Fatalf("POST %s = %d\n%s", path, resp.StatusCode, b)
	}
	return string(b)
}

// TestReleaseChoiceFlow: add with a profile and a pinned version, change
// the choice in the settings, answer the dialog, edit profiles (D-035).
func TestReleaseChoiceFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	up := e.f.NewRepo("fenron", "app")
	up.Commit("README.md", "# App\n", "init")
	up.Push()
	files := func(names ...string) map[string]string {
		m := map[string]string{}
		for _, n := range names {
			m[n] = "data " + n
		}
		return m
	}
	up.AddReleaseFiles("v0.9", "2025-06-01T00:00:00Z", files("app-0.9-linux-x86_64.AppImage", "app-0.9-windows-x64.msi"))
	up.AddReleaseFiles("v1", "2026-01-01T00:00:00Z", files("app-1-linux-x86_64.AppImage", "app-1-linux-aarch64.AppImage",
		"app-1-linux-x86_64.deb", "app-1-windows-x64.msi", "SHA256SUMS.txt"))
	cfg := e.lib.Config()
	one := 1
	cfg.ReleaseProfiles = []config.ReleaseProfile{{Name: "Desktop", ReleasesKeep: &one, OS: []string{"windows", "linux"}, Arch: []string{"x64"}, Formats: []string{"linux/appimage"}}}
	if err := e.lib.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	link := e.f.URL("fenron", "app")

	body := e.get("/add/preview?url="+url.QueryEscape(link), 200)
	for _, want := range []string{"Release-Dateien", "Vorgabe (Standard)", `value="linux/appimage"`, "Bestimmte Versionen behalten", "v0.9"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview misses %q", want)
		}
	}
	// Choosing the profile ticks its boxes and shows the smaller estimate.
	body = e.postHX("/add/releases", "rel_profile", url.Values{"url": {link}, "rel_shown": {"1"}, "rel_profile": {"Desktop"}})
	if !strings.Contains(body, `value="linux/deb">`) || strings.Contains(body, `value="linux/deb" checked`) || !strings.Contains(body, `value="linux/appimage" checked`) {
		t.Errorf("profile not preselected:\n%s", body)
	}
	if !strings.Contains(body, "Neueste 1 Releases: <strong>2 Dateien") {
		t.Errorf("estimate:\n%s", body)
	}

	form := url.Values{"url": {link}, "kind": {"mirror"}, "mode": {"manual"}, "releases": {"on"}, "rel_shown": {"1"},
		"rel_profile": {"Desktop"}, "rel_os": {"windows", "linux"}, "rel_arch": {"x64"}, "rel_format": {"linux/appimage", "windows/msi"},
		"rel_keep": {"1"}, "rel_pin": {"v0.9"}}
	e.post("/add", form, http.StatusSeeOther)
	r, err := e.lib.Get(ctx, "fenron/app")
	if err != nil {
		t.Fatal(err)
	}
	if rf := r.R().ReleaseFiles; rf == nil || rf.Profile != "Desktop" || len(rf.OS) != 0 || r.R().Retention.ReleasesKeep != nil || len(r.R().Retention.Pinned) != 1 {
		t.Fatalf("stored choice = %+v keep %v pinned %v", rf, r.R().Retention.ReleasesKeep, r.R().Retention.Pinned)
	}
	if _, err := e.lib.Sync(ctx, r.R().ID, "t"); err != nil {
		t.Fatal(err)
	}
	id := r.R().ID

	body = e.get("/repo/"+id+"?tab=settings", 200)
	if !strings.Contains(body, `id="release-files"`) || !strings.Contains(body, `<option value="Desktop" selected>`) {
		t.Errorf("settings miss the release section")
	}
	// Only Linux now: the stored .msi files no longer match → dialog.
	resp := e.post("/repo/"+id+"/release-files", url.Values{"rel_shown": {"1"}, "rel_profile": {"Desktop"}, "rel_os": {"linux"},
		"rel_arch": {"x64"}, "rel_format": {"linux/appimage"}}, http.StatusSeeOther)
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "unwanted=1") {
		t.Fatalf("location = %s", loc)
	}
	r, _ = e.lib.Get(ctx, id)
	if rf := r.R().ReleaseFiles; rf == nil || rf.Profile != "" || strings.Join(rf.OS, ",") != "linux" {
		t.Fatalf("own choice = %+v", rf)
	}
	body = e.get(strings.TrimPrefix(loc, e.http.URL), 200)
	if !strings.Contains(body, "Gespeicherte Dateien passen nicht mehr") || !strings.Contains(body, "In den Papierkorb") {
		t.Fatalf("dialog missing")
	}
	items, _ := e.lib.UnwantedFiles(ctx, id)
	if len(items) != 2 {
		t.Fatalf("unwanted = %+v", items)
	}
	trash := url.Values{"action": {"trash"}}
	for _, it := range items {
		trash.Add("item", it.ID)
	}
	e.post("/repo/"+id+"/unwanted", trash, http.StatusSeeOther)
	if items, _ := e.lib.UnwantedFiles(ctx, id); len(items) != 0 {
		t.Errorf("still unwanted: %+v", items)
	}
	if tr, _ := e.lib.Trash(); len(tr) != 2 {
		t.Errorf("trash = %d", len(tr))
	}

	// Profiles in the settings.
	body = e.get("/settings", 200)
	if !strings.Contains(body, "Release-Profile") || !strings.Contains(body, "Windows, Linux · x64 · Linux: AppImage") {
		t.Errorf("profile list missing")
	}
	e.get("/settings/profile", 200)
	e.get("/settings/profile?name=Desktop", 200)
	e.get("/settings/profile?name=Nope", 404)
	e.post("/settings/profile", url.Values{"name": {"Nur Linux"}, "keep": {"2"}, "rel_shown": {"1"}, "rel_os": {"linux"},
		"rel_arch": {"x64", "arm64", "x86", "arm", "riscv64", "other"}, "rel_format": {"linux/appimage", "linux/deb"}}, http.StatusSeeOther)
	p, ok := e.lib.Config().Profile("Nur Linux")
	if !ok || *p.ReleasesKeep != 2 || strings.Join(p.OS, ",") != "linux" || len(p.Arch) != 0 || strings.Join(p.Formats, ",") != "linux/appimage,linux/deb" {
		t.Fatalf("new profile = %+v", p)
	}
	e.post("/settings/profile/default", url.Values{"default": {"Nur Linux"}}, http.StatusSeeOther)
	if e.lib.Config().DefaultReleaseProfile != "Nur Linux" {
		t.Error("default not set")
	}
	e.post("/settings/profile/delete", url.Values{"name": {"Nur Linux"}}, http.StatusSeeOther)
	if _, ok := e.lib.Config().Profile("Nur Linux"); ok || e.lib.Config().DefaultReleaseProfile != "" {
		t.Error("profile not removed")
	}
}
