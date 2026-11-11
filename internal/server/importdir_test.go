// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportFolderForm: the add dialog takes a local folder (D-033) and
// copies it; the original stays.
func TestImportFolderForm(t *testing.T) {
	e := setup(t)
	dir := filepath.Join(t.TempDir(), "voice-model")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644)

	body := e.get("/add/preview?url="+url.QueryEscape(dir), 200)
	if !strings.Contains(body, "Lokaler Ordner") || !strings.Contains(body, `name="model_id"`) {
		t.Fatalf("preview:\n%s", body)
	}
	if body := e.get("/add/preview?url="+url.QueryEscape("smb://nas/modelle"), 200); !strings.Contains(body, "Freigaben bitte als Pfad") {
		t.Error("smb hint missing")
	}
	resp := e.post("/add", url.Values{"url": {dir}, "folder_mode": {"copy"}, "tags": {"tts"}}, 303)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/repo/") {
		t.Fatalf("redirect = %s", loc)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Error("copy removed the original")
	}
	if body := e.get("/?tag=modell", 200); !strings.Contains(body, "voice-model") {
		t.Error("imported model not in the library")
	}
}
