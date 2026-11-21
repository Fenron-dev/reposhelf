// SPDX-License-Identifier: GPL-3.0-or-later

//go:build desktop

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wailsassets "github.com/wailsapp/wails/v2/pkg/assetserver"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/desktop"
	"github.com/Fenron-dev/reposhelf/internal/server"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
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

func TestWindowFollowsRedirectAfterPost(t *testing.T) {
	app := &App{}
	app.handler = desktop.FollowRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/repo/neu", http.StatusSeeOther)
	}))
	srv, err := wailsassets.NewAssetServer("", *assetOptions(app), false, nil, fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "wails://wails/add", strings.NewReader("url=x")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "url=/repo/neu") {
		t.Fatalf("POST answer = %d %s", rec.Code, rec.Body.String())
	}
}

// TestWindowIsTrustedByTheServer: the window's Host is Wails' own name,
// not this machine's; the real server must still treat it as the local
// user (R-011), also for forms.
func TestWindowIsTrustedByTheServer(t *testing.T) {
	lib, err := core.Open(context.Background(), core.Options{DataDir: t.TempDir(), Git: testutil.NewForge(t).Git, NoKeychain: true,
		LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	srv, err := server.New(lib, server.Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{handler: srv.Handler()}
	if body := window(t, app, "/settings"); !strings.Contains(body, "Einstellungen") {
		t.Fatalf("settings not shown to the window:\n%s", body)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "wails://wails/create", strings.NewReader("name=probe"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "wails://wails")
	app.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("window form refused: %s", rec.Body.String())
	}
	if _, err := lib.Get(context.Background(), "local/projekte/probe"); err != nil {
		t.Errorf("window form did not create the project: %v", err)
	}
}
