// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"net/http"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/update"
)

// Self-update card in the settings (V-15, T-9.32, D-036).

// installTimeout bounds downloading and replacing (large DMG on a slow line).
const installTimeout = 30 * time.Minute

type updateView struct {
	Current    string
	Flavor     update.Flavor
	State      update.State
	Available  *update.Release
	CanInstall bool
	Check      bool // daily check switched on
}

func (s *Server) updateView() *updateView {
	u := s.Updates
	if u == nil {
		return nil
	}
	v := &updateView{Current: u.Current, Flavor: u.Flavor, State: u.State(), CanInstall: u.CanInstall(), Check: s.Lib.Config().Updates.Check}
	if rel, ok := u.Available(); ok {
		v.Available = &rel
	}
	return v
}

// updateAvailable is the newer version for the sidebar ("" if none).
func (s *Server) updateAvailable() string {
	if s.Updates == nil {
		return ""
	}
	if rel, ok := s.Updates.Available(); ok && s.Updates.State().Installed == "" {
		return rel.Tag
	}
	return ""
}

func (s *Server) updateAction(w http.ResponseWriter, r *http.Request) {
	back := func(key, msg string) { redirect(w, r, withMsg("/settings#aktualisierung", key, msg)) }
	u := s.Updates
	if u == nil {
		back("fehler", "Aktualisierung ist hier nicht verfügbar.")
		return
	}
	r.ParseForm()
	switch r.Form.Get("action") {
	case "check":
		if _, err := u.Check(r.Context()); err != nil {
			back("fehler", "Prüfung fehlgeschlagen: "+ErrText(err))
			return
		}
		if rel, ok := u.Available(); ok {
			back("ok", "Neue Version "+rel.Tag+" verfügbar.")
			return
		}
		back("ok", "RepoShelf ist aktuell.")
	case "install":
		// Not bound to the request: closing the page must not cut the download.
		ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
		defer cancel()
		msg, err := u.Install(ctx)
		if err != nil {
			back("fehler", "Nicht installiert: "+ErrText(err))
			return
		}
		back("ok", msg)
	default:
		cfg := s.Lib.Config()
		cfg.Updates.Check = r.Form.Get("check") == "on"
		if err := s.Lib.SaveConfig(cfg); err != nil {
			s.failErr(w, r, err)
			return
		}
		back("ok", "Gespeichert.")
	}
}
