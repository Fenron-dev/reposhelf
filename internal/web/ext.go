// SPDX-License-Identifier: GPL-3.0-or-later

package web

import "net/http"

// Extensions are optional parts provided by other packages (builds,
// instance sync); nil hides the corresponding pages.
type Extensions struct {
	Builds Builds
	Peers  Peers
}

// Builds is implemented by the runner package (Phase 6/7).
type Builds interface {
	Routes(s *Server, mux *http.ServeMux)
	// Panel returns data for the build/capsule card of a repository.
	Panel(r *http.Request, repoID string) any
}

// Peers is implemented by the peer package (Phase 8).
type Peers interface {
	Routes(s *Server, mux *http.ServeMux)
}

func (s *Server) extRoutes(mux *http.ServeMux) {
	if s.Ext.Builds != nil {
		s.Ext.Builds.Routes(s, mux)
	}
	if s.Ext.Peers != nil {
		s.Ext.Peers.Routes(s, mux)
	}
}

// Render exposes page rendering to extensions.
func (s *Server) Render(w http.ResponseWriter, r *http.Request, name, title, nav string, data any) {
	s.render(w, r, name, title, nav, data)
}

// Fail exposes error pages to extensions.
func (s *Server) Fail(w http.ResponseWriter, r *http.Request, err error) { s.failErr(w, r, err) }

// Need exposes the rights check to extensions.
func (s *Server) Need(right string, h http.HandlerFunc) http.Handler { return s.need(right, h) }

// Redirect exposes the POST/redirect helper to extensions.
func Redirect(w http.ResponseWriter, r *http.Request, to string) { redirect(w, r, to) }

// WithMsg adds a flash message to a URL.
func WithMsg(to, key, msg string) string { return withMsg(to, key, msg) }

// IdentityOf exposes the identity resolver to extensions.
func (s *Server) IdentityOf(r *http.Request) (string, bool) {
	tok, ok := s.Identity(r)
	return tok.Name, ok
}
