// SPDX-License-Identifier: GPL-3.0-or-later

// Package server assembles the HTTP surfaces – web UI, REST API, Git HTTP
// and MCP over Streamable HTTP – on one listener and decides who a request
// comes from (tokens, login sessions, trusted local access).
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fenron-dev/reposhelf/internal/api"
	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/githttp"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/mcp"
	"github.com/Fenron-dev/reposhelf/internal/web"
)

// TokenFile holds hashed access tokens.
const TokenFile = "tokens.yaml"

const (
	sessionCookie = "reposhelf_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// Options configure the server.
type Options struct {
	Log       *slog.Logger
	LookupEnv func(string) (string, bool)
	Ext       web.Extensions
	MCPBuilds mcp.Builds
	// Extra mounts additional handlers (e.g. peer sync endpoints).
	Extra map[string]http.Handler
	// ExtraAuth accepts additional secrets (temporary peer tokens).
	ExtraAuth func(secret string) (auth.Token, bool)
	// BaseURL is the address shown for clone links when no public URL is
	// configured and the request host is not reachable from outside (the
	// desktop window's internal host).
	BaseURL string
}

// Server is the HTTP front of a library.
type Server struct {
	Lib    *core.Library
	Tokens *auth.Store
	Log    *slog.Logger
	Web    *web.Server
	opts   Options

	mu       sync.Mutex
	sessions map[string]session
}

type session struct {
	token   auth.Token
	expires time.Time
}

type ctxKey struct{}

type ident struct {
	tok    auth.Token
	authed bool
}

// New prepares the server.
func New(lib *core.Library, o Options) (*Server, error) {
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.Log == nil {
		o.Log = lib.Log
	}
	admin, _ := o.LookupEnv(auth.EnvAdminToken)
	tokens, err := auth.Open(lib.Layout.Path(layout.DirInternal, TokenFile), strings.TrimSpace(admin), lib.Now)
	if err != nil {
		return nil, err
	}
	s := &Server{Lib: lib, Tokens: tokens, Log: o.Log, opts: o, sessions: map[string]session{}}
	s.Web = &web.Server{Lib: lib, Tokens: tokens, Identity: s.identity, Log: o.Log, Ext: o.Ext,
		Login: s.login, Logout: s.logout, CloneBase: s.base}
	return s, nil
}

// Handler returns the complete HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.Handle(githttp.Prefix, &githttp.Handler{Lib: s.Lib, Identity: s.identity, Log: s.Log})
	mux.Handle("/api/", (&api.Handler{Lib: s.Lib, Identity: s.identity}).Routes())
	mux.Handle("/mcp", s.mcpHandler())
	for p, h := range s.opts.Extra {
		mux.Handle(p, h)
	}
	mux.Handle("/", s.Web.Handler())
	return s.withIdentity(mux)
}

func (s *Server) mcpHandler() http.Handler {
	h := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		tok, _ := s.identity(r)
		return mcp.New(s.Lib, mcp.Options{Rights: tok.Rights, Client: tok.Name, CloneBase: func() string { return s.base(r) }, Builds: s.opts.MCPBuilds})
	}, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, authed := s.identity(r)
		if !tok.Has(auth.Read) || (!authed && tok.ID == auth.Anonymous.ID) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="RepoShelf MCP"`)
			http.Error(w, "MCP braucht ein Token (Authorization: Bearer …)", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// withIdentity resolves the caller once per request.
func (s *Server) withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := s.resolve(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

func (s *Server) identity(r *http.Request) (auth.Token, bool) {
	if id, ok := r.Context().Value(ctxKey{}).(ident); ok {
		return id.tok, id.authed
	}
	id := s.resolve(r)
	return id.tok, id.authed
}

// resolve: explicit token > session > trusted local > anonymous read.
func (s *Server) resolve(r *http.Request) ident {
	if secret := bearer(r); secret != "" {
		if tok, err := s.Tokens.Check(secret); err == nil {
			return ident{tok: tok, authed: true}
		}
		if s.opts.ExtraAuth != nil {
			if tok, ok := s.opts.ExtraAuth(secret); ok {
				return ident{tok: tok, authed: true}
			}
		}
		return ident{} // wrong token: no fallback to local trust
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		sess, ok := s.sessions[c.Value]
		s.mu.Unlock()
		if ok && time.Now().Before(sess.expires) {
			return ident{tok: sess.token, authed: true}
		}
	}
	if s.isLocal(r) {
		return ident{tok: auth.Local, authed: true}
	}
	if s.Lib.Config().Server.AnonymousRead {
		return ident{tok: auth.Anonymous}
	}
	return ident{}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(t)
	}
	if t, ok := strings.CutPrefix(h, "token "); ok {
		return strings.TrimSpace(t)
	}
	if _, pw, ok := r.BasicAuth(); ok {
		return pw
	}
	return ""
}

// trustedKey marks a request the embedding program vouches for.
type trustedKey struct{}

// MarkLocal marks a request as coming from this program's own window
// (desktop app): it is trusted whatever its Host header says.
func MarkLocal(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), trustedKey{}, true))
}

// isLocal trusts requests from this machine (desktop app, CLI, health
// checks) – but not when a proxy forwarded them: a tunnel on the same host
// would otherwise make everyone "local". The Host header must name this
// machine too: a web page whose own domain resolves to 127.0.0.1 (DNS
// rebinding) reaches the loopback address with its domain as Host and
// would otherwise act as the local user (R-011).
func (s *Server) isLocal(r *http.Request) bool {
	if r.Context().Value(trustedKey{}) != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-Ip") != "" || r.Header.Get("Cf-Connecting-Ip") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && s.localHost(r.Host)
}

// localHost reports whether a Host header names this machine: localhost,
// a loopback address, or the configured public address.
func (s *Server) localHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	if pu := s.Lib.Config().Server.PublicURL; pu != "" {
		if u, err := url.Parse(pu); err == nil && u.Hostname() != "" && strings.EqualFold(u.Hostname(), h) {
			return true
		}
	}
	return false
}

// base returns scheme://host for links shown to the user.
func (s *Server) base(r *http.Request) string {
	if u := s.Lib.Config().Server.PublicURL; u != "" {
		return strings.TrimSuffix(u, "/")
	}
	if s.opts.BaseURL != "" {
		return strings.TrimSuffix(s.opts.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	tok, err := s.Tokens.Check(r.Form.Get("token"))
	if err != nil {
		http.Redirect(w, r, "/login?fehler="+urlEsc("Token unbekannt oder widerrufen."), http.StatusSeeOther)
		return
	}
	var b [24]byte
	rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	s.mu.Lock()
	s.sessions[id] = session{token: tok, expires: time.Now().Add(sessionTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil, Expires: time.Now().Add(sessionTTL)})
	s.Lib.Record(core.Activity{Actor: "web:" + tok.Name, Action: "login"})
	next := r.Form.Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func urlEsc(s string) string { return url.QueryEscape(s) }

// Run serves on addr until ctx ends, with background syncing.
func (s *Server) Run(ctx context.Context, addr string, ready func(addr string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 20 * time.Second, IdleTimeout: 2 * time.Minute}
	s.Lib.StartBackground()
	if ready != nil {
		ready(ln.Addr().String())
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx) // shutting down anyway
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
