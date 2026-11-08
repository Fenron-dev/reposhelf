// SPDX-License-Identifier: GPL-3.0-or-later

// Package desktop holds the parts of the desktop shell that do not depend
// on Wails, so they can be tested everywhere: the first-run setup that asks
// for the data folder (KONZEPT 4.4: never choose a location silently).
package desktop

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/Fenron-dev/reposhelf/internal/datadir"
)

// Setup serves the setup page until a data folder is chosen.
type Setup struct {
	// Portable is the folder next to the app (portable mode).
	Portable string
	// UserConfigDir is where the pointer file goes (os.UserConfigDir).
	UserConfigDir func() (string, error)
	// Open starts RepoShelf with the chosen folder.
	Open func(dir string) error

	mu     sync.Mutex
	reason string
}

// SetReason shows why setup is needed (e.g. folder unreachable).
func (s *Setup) SetReason(r string) {
	s.mu.Lock()
	s.reason = r
	s.mu.Unlock()
}

// Reason returns the current message.
func (s *Setup) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// ServeHTTP shows the form (GET) or applies the choice (POST /setup).
func (s *Setup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/setup" {
		s.submit(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = setupPage.Execute(w, map[string]any{"Reason": s.Reason(), "Portable": s.Portable})
}

func (s *Setup) submit(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	dir := r.Form.Get("dir")
	portable := r.Form.Get("mode") == "portable"
	if portable {
		dir = s.Portable
	}
	fail := func(msg string) {
		s.SetReason(msg)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
	if dir == "" || !filepath.IsAbs(dir) {
		fail("Bitte einen absoluten Ordnerpfad angeben.")
		return
	}
	// The user chose this folder explicitly; creating it is no silent choice.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err.Error())
		return
	}
	if !portable && s.UserConfigDir != nil {
		if cfg, err := s.UserConfigDir(); err == nil {
			if err := datadir.WritePointer(cfg, dir); err != nil {
				fail(err.Error())
				return
			}
		}
	}
	if err := s.Open(dir); err != nil {
		fail(err.Error())
		return
	}
	s.SetReason("")
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
