// SPDX-License-Identifier: GPL-3.0-or-later

//go:build desktop

// Command reposhelf-desktop wraps the RepoShelf web UI in a native window
// (Wails v2, D-019). It is the same program as the server: the library is
// opened in-process, the UI is served through Wails' asset server, and the
// HTTP server keeps running on 127.0.0.1 for git clients and agents (MCP).
// The core never imports Wails (AGENTS.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
	"github.com/Fenron-dev/reposhelf/internal/desktop"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/peer"
	"github.com/Fenron-dev/reposhelf/internal/runner"
	"github.com/Fenron-dev/reposhelf/internal/server"
	"github.com/Fenron-dev/reposhelf/internal/web"
)

// App is the desktop application state; its exported methods are callable
// from the setup page (Wails bindings).
type App struct {
	ctx     context.Context
	mu      sync.Mutex
	lib     *core.Library
	handler http.Handler
	cancel  context.CancelFunc
	setup   *desktop.Setup
}

func main() {
	app := &App{}
	app.setup = &desktop.Setup{Portable: portablePath(), UserConfigDir: os.UserConfigDir, Open: app.open}
	err := wails.Run(&options.App{
		Title:            "RepoShelf",
		Width:            1320,
		Height:           880,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 14, G: 16, B: 20, A: 255},
		AssetServer:      assetOptions(app),
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		Mac:              &mac.Options{TitleBar: mac.TitleBarDefault(), About: &mac.AboutInfo{Title: "RepoShelf", Message: "Archiv und Bereitstellung für Git-Repositories · GPL-3.0-or-later"}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// assetOptions serves every page through RepoShelf's own handler. No
// static Assets on purpose: Wails serves files from Assets before the
// handler, so a placeholder index.html would hide the real UI (the window
// stayed white in v0.1.4).
func assetOptions(app *App) *assetserver.Options {
	return &assetserver.Options{Handler: app}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	loc, err := datadir.Resolve(datadir.OSEnv(""))
	switch {
	case errors.Is(err, datadir.ErrNotConfigured):
		a.setup.SetReason("Noch kein Datenordner eingerichtet.")
		return
	case err != nil:
		// Never fall back silently (invariant 5): show the problem.
		a.setup.SetReason("Datenordner nicht erreichbar: " + err.Error())
		return
	}
	if err := a.open(loc.Path); err != nil {
		a.setup.SetReason(err.Error())
	}
}

// open starts the library and the background HTTP server.
func (a *App) open(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := layout.Init(dir); err != nil {
		return err
	}
	log, _, err := logx.Open(filepath.Join(dir, layout.DirInternal, layout.DirLogs), slog.LevelInfo, nil)
	if err != nil {
		return err
	}
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	lib, err := core.Open(a.ctx, core.Options{DataDir: dir, ExeDir: exeDir, Logger: log})
	if err != nil {
		return err
	}
	builds := runner.New(lib, log)
	builds.Start(a.ctx)
	peers, err := peer.New(lib, log)
	if err != nil {
		lib.Close()
		return err
	}
	peers.PublicURL = func() string { return "http://" + lib.Config().Server.Listen }
	peers.SetBuilds(builds)
	peers.RegisterRemotes(builds)
	go peers.Schedule(a.ctx)
	srv, err := server.New(lib, server.Options{Log: log, Ext: web.Extensions{Builds: builds, Peers: peers}, MCPBuilds: runner.MCP{M: builds},
		ExtraAuth: peers.CheckTemp, Extra: map[string]http.Handler{"/peer/": peers.Handler()},
		BaseURL: "http://" + lib.Config().Server.Listen})
	if err != nil {
		lib.Close()
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	go func() {
		if err := srv.Run(ctx, lib.Config().Server.Listen, nil); err != nil {
			log.Warn("http server", "err", err)
		}
	}()
	lib.StartBackground()
	a.lib, a.handler, a.cancel = lib, desktop.FollowRedirects(srv.Handler()), cancel
	return nil
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	if a.lib != nil {
		a.lib.Close()
	}
}

// ServeHTTP serves the UI. Requests come from the app's own window, so
// they are marked as local (trusted) requests.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	h := a.handler
	a.mu.Unlock()
	if h == nil {
		desktop.FollowRedirects(a.setup).ServeHTTP(w, r)
		return
	}
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Del("X-Forwarded-For")
	h.ServeHTTP(w, r)
}

func portablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(datadir.PortableBase(exe), datadir.PortableDirName)
}

// ChooseFolder opens the native folder dialog (bound to the setup page).
func (a *App) ChooseFolder() string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Datenordner für RepoShelf wählen", CanCreateDirectories: true})
	if err != nil {
		return ""
	}
	return dir
}
