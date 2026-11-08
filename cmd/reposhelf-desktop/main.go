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
	"embed"
	"errors"
	"fmt"
	"html/template"
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
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/runner"
	"github.com/Fenron-dev/reposhelf/internal/server"
	"github.com/Fenron-dev/reposhelf/internal/web"
)

//go:embed frontend/dist
var assets embed.FS

// App is the desktop application state; its exported methods are callable
// from the setup page (Wails bindings).
type App struct {
	ctx     context.Context
	mu      sync.Mutex
	lib     *core.Library
	handler http.Handler
	cancel  context.CancelFunc
	setup   string // reason why setup is shown
}

func main() {
	app := &App{}
	err := wails.Run(&options.App{
		Title:            "RepoShelf",
		Width:            1320,
		Height:           880,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: &options.RGBA{R: 14, G: 16, B: 20, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets, Handler: app},
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

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	loc, err := datadir.Resolve(datadir.OSEnv(""))
	switch {
	case errors.Is(err, datadir.ErrNotConfigured):
		a.setup = "Noch kein Datenordner eingerichtet."
		return
	case err != nil:
		// Never fall back silently (invariant 5): show the problem.
		a.setup = "Datenordner nicht erreichbar: " + err.Error()
		return
	}
	if err := a.open(loc.Path); err != nil {
		a.setup = err.Error()
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
	srv, err := server.New(lib, server.Options{Log: log, Ext: web.Extensions{Builds: builds}, MCPBuilds: runner.MCP{M: builds}})
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
	a.lib, a.handler, a.cancel, a.setup = lib, srv.Handler(), cancel, ""
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
	h, reason := a.handler, a.setup
	a.mu.Unlock()
	if h == nil {
		if r.Method == http.MethodPost && r.URL.Path == "/setup" {
			a.setupSubmit(w, r)
			return
		}
		setupPage.Execute(w, map[string]any{"Reason": reason, "Portable": a.portablePath()})
		return
	}
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Del("X-Forwarded-For")
	h.ServeHTTP(w, r)
}

func (a *App) portablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(datadir.PortableBase(exe), datadir.PortableDirName)
}

func (a *App) setupSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	dir := r.Form.Get("dir")
	portable := r.Form.Get("mode") == "portable"
	if portable {
		dir = a.portablePath()
	}
	if dir == "" || !filepath.IsAbs(dir) {
		a.setup = "Bitte einen absoluten Ordnerpfad angeben."
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// The user chose this folder explicitly; creating it is no silent choice.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.setup = err.Error()
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !portable {
		if cfg, err := os.UserConfigDir(); err == nil {
			if err := datadir.WritePointer(cfg, dir); err != nil {
				a.setup = err.Error()
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
	}
	if err := a.open(dir); err != nil {
		a.setup = err.Error()
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ChooseFolder opens the native folder dialog (bound to the setup page).
func (a *App) ChooseFolder() string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Datenordner für RepoShelf wählen", CanCreateDirectories: true})
	if err != nil {
		return ""
	}
	return dir
}

var setupPage = template.Must(template.New("setup").Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><title>RepoShelf einrichten</title>
<style>body{margin:0;background:#0E1014;color:#E7E9EE;font:14px/1.5 Inter,system-ui,sans-serif;display:flex;justify-content:center;padding:48px}
.card{background:#12151B;border:1px solid #232833;border-radius:18px;padding:28px;max-width:640px;width:100%}
h1{font-family:"Space Grotesk",Inter,sans-serif;margin:0 0 8px}p{color:#C9CED8}.muted{color:#9BA3AF}
.opt{display:block;border:1px solid #2A303C;border-radius:12px;padding:14px;margin:12px 0;background:#151821}
input[type=text]{width:100%;box-sizing:border-box;min-height:40px;border-radius:10px;border:1px solid #2A303C;background:#0E1014;color:#E7E9EE;padding:6px 12px;margin-top:8px}
button{min-height:42px;padding:0 18px;border-radius:10px;border:0;background:#F2B544;color:#1A1206;font-weight:600;cursor:pointer;margin-top:8px}
.ghost{background:#151821;color:#E7E9EE;border:1px solid #2A303C}.err{border:1px solid #FCA5A5;border-radius:10px;padding:10px 14px}</style></head>
<body><form class="card" method="post" action="/setup">
<h1>Willkommen bei RepoShelf</h1>
{{if .Reason}}<p class="err">{{.Reason}}</p>{{end}}
<p>Wo sollen deine Repos, Metadaten und Releases liegen? RepoShelf wählt den Ort nie von selbst.</p>
<label class="opt"><input type="radio" name="mode" value="portable"> <strong>Portabel neben der App</strong><br><span class="muted">{{.Portable}} – gut für USB-Sticks</span></label>
<label class="opt"><input type="radio" name="mode" value="dir" checked> <strong>Eigenen Ordner wählen</strong>
<input type="text" id="dir" name="dir" placeholder="/Users/…/RepoShelf-Daten"><br>
<button type="button" class="ghost" id="choose">Ordner auswählen …</button></label>
<button type="submit">Einrichten und starten</button>
</form>
<script>document.getElementById("choose").onclick=function(){window.go.main.App.ChooseFolder().then(function(d){if(d)document.getElementById("dir").value=d;});};</script>
</body></html>`))
