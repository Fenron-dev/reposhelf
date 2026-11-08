// SPDX-License-Identifier: GPL-3.0-or-later

//go:build desktop

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wailsassets "github.com/wailsapp/wails/v2/pkg/assetserver"

	"github.com/Fenron-dev/reposhelf/internal/desktop"
)

type fakeRuntime struct{}

func (fakeRuntime) DesktopIPC() []byte       { return []byte("// ipc") }
func (fakeRuntime) WebsocketIPC() []byte     { return nil }
func (fakeRuntime) RuntimeDesktopJS() []byte { return []byte("// runtime") }

// window fetches a path the way the Wails window does: through Wails'
// asset server with RepoShelf's options.
func window(t *testing.T, app *App, path string) string {
	t.Helper()
	srv, err := wailsassets.NewAssetServer("", *assetOptions(app), false, nil, fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "wails://wails"+path, nil))
	b, _ := io.ReadAll(rec.Body)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, rec.Code)
	}
	return string(b)
}

func TestWindowShowsSetupPage(t *testing.T) {
	app := &App{}
	app.setup = &desktop.Setup{Portable: "/x/RepoShelf-Daten", Open: func(string) error { return nil }}
	body := window(t, app, "/")
	if !strings.Contains(body, "Willkommen bei RepoShelf") {
		t.Fatalf("setup page not shown in window:\n%s", body)
	}
	if !strings.Contains(body, "/wails/runtime.js") {
		t.Error("Wails runtime not injected (folder dialog would not work)")
	}
}

func TestWindowShowsLibrary(t *testing.T) {
	app := &App{}
	app.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr != "127.0.0.1:1" {
			t.Errorf("window request not marked local: %s", r.RemoteAddr)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<!doctype html><html><head></head><body>Bibliothek " + r.URL.Path + "</body></html>"))
	})
	if body := window(t, app, "/"); !strings.Contains(body, "Bibliothek /") {
		t.Fatalf("library not shown:\n%s", body)
	}
	if body := window(t, app, "/repo/abc"); !strings.Contains(body, "Bibliothek /repo/abc") {
		t.Fatalf("sub page not routed:\n%s", body)
	}
}
