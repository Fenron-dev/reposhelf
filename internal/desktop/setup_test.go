// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/datadir"
)

func post(s *Setup, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestSetupFlow(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	var opened string
	s := &Setup{Portable: filepath.Join(root, "app", datadir.PortableDirName),
		UserConfigDir: func() (string, error) { return profile, nil },
		Open:          func(dir string) error { opened = dir; return nil }}
	s.SetReason("Noch kein Datenordner eingerichtet.")

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "Willkommen bei RepoShelf") || !strings.Contains(body, "Noch kein Datenordner") || !strings.Contains(body, s.Portable) {
		t.Fatalf("setup page:\n%s", body)
	}

	// Relative paths are refused, nothing is opened.
	post(s, url.Values{"mode": {"dir"}, "dir": {"relativ"}})
	if opened != "" || !strings.Contains(s.Reason(), "absoluten") {
		t.Errorf("relative accepted: %q %q", opened, s.Reason())
	}

	// Own folder: created, pointer written, opened.
	dir := filepath.Join(root, "RepoShelf-Daten")
	rec = post(s, url.Values{"mode": {"dir"}, "dir": {dir}})
	if rec.Code != http.StatusSeeOther || opened != dir || s.Reason() != "" {
		t.Fatalf("own folder: code %d opened %q reason %q", rec.Code, opened, s.Reason())
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Error("folder not created")
	}
	b, err := os.ReadFile(datadir.PointerPath(profile))
	if err != nil || strings.TrimSpace(string(b)) != dir {
		t.Errorf("pointer = %q %v", b, err)
	}

	// Portable: next to the app, no pointer.
	os.Remove(datadir.PointerPath(profile))
	post(s, url.Values{"mode": {"portable"}})
	if opened != s.Portable {
		t.Errorf("portable opened %q", opened)
	}
	if _, err := os.Stat(datadir.PointerPath(profile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("portable mode wrote a pointer")
	}

	// A failing open is shown, not swallowed.
	s.Open = func(string) error { return errors.New("kaputt") }
	post(s, url.Values{"mode": {"dir"}, "dir": {dir}})
	if !strings.Contains(s.Reason(), "konnte nicht geöffnet werden: kaputt") {
		t.Errorf("reason = %q", s.Reason())
	}
}
