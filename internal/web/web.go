// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/buildinfo"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Identity tells who made a request; authed is false for anonymous access.
type Identity func(r *http.Request) (tok auth.Token, authed bool)

// Server is the web UI.
type Server struct {
	Lib      *core.Library
	Tokens   *auth.Store
	Identity Identity
	Log      *slog.Logger
	Ext      Extensions
	// Login handles POST /login (set by the server package).
	Login  http.HandlerFunc
	Logout http.HandlerFunc
	// CloneBase returns scheme://host for clone URLs of a request.
	CloneBase func(r *http.Request) string

	tmpl     *template.Template
	md       goldmark.Markdown
	once     sync.Once
	reviewMu sync.Mutex
	reviewed bool // cleanup dialog answered in this run ("beim nächsten Start erneut fragen")
}

// Handler returns the routes of the web UI.
func (s *Server) Handler() http.Handler {
	s.once.Do(s.init)
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /login", s.loginPage)
	if s.Login != nil {
		mux.HandleFunc("POST /login", s.Login)
	}
	if s.Logout != nil {
		mux.HandleFunc("POST /logout", s.Logout)
	}

	read := func(h http.HandlerFunc) http.Handler { return s.need(auth.Read, h) }
	write := func(h http.HandlerFunc) http.Handler { return s.need(auth.Write, h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.need(auth.Admin, h) }

	mux.Handle("GET /{$}", read(s.library))
	mux.Handle("GET /search", read(s.searchJSON))
	mux.Handle("GET /events", read(s.eventsSSE))
	mux.Handle("GET /jobs", read(s.jobsPartial))

	mux.Handle("GET /repo/{id}", read(s.repoPage))
	mux.Handle("GET /repo/{id}/tree/{path...}", read(s.repoTree))
	mux.Handle("GET /repo/{id}/blob/{path...}", read(s.repoBlob))
	mux.Handle("GET /repo/{id}/raw/{path...}", read(s.repoRaw))
	mux.Handle("GET /repo/{id}/asset", read(s.repoAsset))
	mux.Handle("POST /repo/{id}/sync", write(s.repoSync))
	mux.Handle("POST /repo/{id}/tags", write(s.repoTags))
	mux.Handle("POST /repo/{id}/collections", write(s.repoCollections))
	mux.Handle("POST /repo/{id}/relation", write(s.repoRelation))
	mux.Handle("POST /repo/{id}/notes", write(s.repoNotes))
	mux.Handle("POST /repo/{id}/settings", write(s.repoSettings))
	mux.Handle("POST /repo/{id}/pin", write(s.repoPin))
	mux.Handle("POST /repo/{id}/fork", write(s.repoFork))
	mux.Handle("POST /repo/{id}/mark", admin(s.repoMark))

	mux.Handle("GET /add", write(s.addPage))
	mux.Handle("GET /add/preview", write(s.addPreview))
	mux.Handle("POST /add", write(s.addSubmit))
	mux.Handle("POST /create", write(s.createLocal))

	mux.Handle("GET /tags", read(s.tagsPage))
	mux.Handle("POST /tags/rename", write(s.tagsRename))
	mux.Handle("GET /collections/new", write(s.collectionEdit))
	mux.Handle("GET /collections/{id}", read(s.collectionPage))
	mux.Handle("GET /collections/{id}/edit", write(s.collectionEdit))
	mux.Handle("POST /collections/{id}", write(s.collectionSave))
	mux.Handle("GET /graph", read(s.graphPage))
	mux.Handle("GET /graph.json", read(s.graphJSON))
	mux.Handle("GET /activity", read(s.activityPage))

	mux.Handle("GET /cleanup", admin(s.cleanupPage))
	mux.Handle("POST /cleanup", admin(s.cleanupSubmit))
	mux.Handle("GET /trash", admin(s.trashPage))
	mux.Handle("POST /trash/restore", admin(s.trashRestore))
	mux.Handle("POST /trash/purge", admin(s.trashPurge))
	mux.Handle("GET /settings", admin(s.settingsPage))
	mux.Handle("POST /settings", admin(s.settingsSave))
	mux.Handle("GET /access", admin(s.accessPage))
	mux.Handle("POST /access/tokens", admin(s.tokenCreate))
	mux.Handle("POST /access/tokens/{id}/revoke", admin(s.tokenRevoke))

	s.extRoutes(mux)
	return securityHeaders(sameOrigin(mux))
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// securityHeaders: repository content (READMEs) is foreign data; scripts
// only from this server, no framing by other sites.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'self'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests from other sites (CSRF): the
// Origin (or Referer) of a POST must match the Host.
func sameOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			src := r.Header.Get("Origin")
			if src == "" {
				src = r.Header.Get("Referer")
			}
			u, err := url.Parse(src)
			if src == "" || err != nil || u.Host != r.Host {
				http.Error(w, "Anfrage von fremder Seite abgelehnt", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// need checks a right; anonymous users are sent to the login page.
func (s *Server) need(right string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, authed := s.Identity(r)
		if !tok.Has(right) {
			if !authed && r.Method == http.MethodGet {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
				return
			}
			s.fail(w, r, http.StatusForbidden, "Dafür fehlt die Berechtigung („"+rightLabel(right)+"“).")
			return
		}
		h(w, r)
	})
}

func rightLabel(r string) string {
	return map[string]string{auth.Read: "lesen", auth.Write: "schreiben", auth.Build: "bauen", auth.Admin: "verwalten"}[r]
}

func (s *Server) init() {
	s.tmpl = template.Must(template.New("").Funcs(s.funcs()).ParseFS(templateFS, "templates/*.html"))
	s.md = goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Raw HTML in READMEs is not rendered (foreign content, XSS).
		goldmark.WithRendererOptions(html.WithHardWraps(), html.WithXHTML()),
	)
}

// page is the data of every full page.
type page struct {
	Title    string
	Nav      string
	Identity auth.Token
	Authed   bool
	Sidebar  sidebar
	Flash    string
	Error    string
	Data     any
	Version  string
}

type sidebar struct {
	Repos, Tags, Trash int
	Size               int64
	Collections        []core.Collection
	Jobs               int
	Instance           string
	Builds             bool
	Peers              bool
	Pending            int
	NextSync           *time.Time
	Paused             bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name, title, nav string, data any) {
	tok, authed := s.Identity(r)
	p := page{Title: title, Nav: nav, Identity: tok, Authed: authed, Data: data, Version: buildinfo.String(),
		Flash: r.URL.Query().Get("ok"), Error: r.URL.Query().Get("fehler")}
	p.Sidebar = s.sidebar(r)
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, p); err != nil {
		s.Log.Error("template", "name", name, "err", err)
		http.Error(w, "Darstellungsfehler: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// partial renders a template fragment (htmx swaps).
func (s *Server) partial(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.Log.Error("template", "name", name, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) sidebar(r *http.Request) sidebar {
	ctx := r.Context()
	var sb sidebar
	sb.Instance = s.Lib.Config().Instance.Name
	sb.Paused = s.Lib.Config().Sync.Paused
	if t, err := s.Lib.Store.Totals(ctx); err == nil {
		sb.Repos, sb.Size = t.Repos, t.SizeBytes
	}
	if tags, err := s.Lib.Store.Tags(ctx); err == nil {
		sb.Tags = len(tags)
	}
	if cols, err := s.Lib.Collections(ctx); err == nil {
		sb.Collections = cols
	}
	if tr, err := s.Lib.Trash(); err == nil {
		sb.Trash = len(tr)
	}
	sb.Jobs = len(s.Lib.Jobs())
	sb.Builds = s.Ext.Builds != nil
	sb.Peers = s.Ext.Peers != nil
	sb.NextSync = s.nextSync(r)
	return sb
}

func (s *Server) nextSync(r *http.Request) *time.Time {
	entries, err := s.Lib.List(r.Context(), store.Query{})
	if err != nil {
		return nil
	}
	var next *time.Time
	for _, e := range entries {
		if t, ok := s.Lib.NextCheck(e); ok {
			if next == nil || t.Before(*next) {
				tt := t
				next = &tt
			}
		}
	}
	return next
}

// fail shows an error page (or a fragment for htmx requests).
func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		fmt.Fprintf(w, `<div class="alert alert-error" role="alert">%s</div>`, template.HTMLEscapeString(msg))
		return
	}
	w.WriteHeader(code)
	s.render(w, r, "error", "Fehler", "", msg)
}

// failErr maps library errors to messages and status codes.
func (s *Server) failErr(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, core.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, core.ErrInvalid), errors.Is(err, core.ErrExists), errors.Is(err, sources.ErrBadURL):
		code = http.StatusBadRequest
	case errors.Is(err, core.ErrForbidden), errors.Is(err, core.ErrReadOnly):
		code = http.StatusForbidden
	}
	s.fail(w, r, code, ErrText(err))
}

// ErrText is the German message for an error.
func ErrText(err error) string {
	switch {
	case errors.Is(err, core.ErrExists):
		return "Dieses Repo ist schon vorhanden: " + err.Error()
	case errors.Is(err, core.ErrNotFound):
		return "Nicht gefunden: " + err.Error()
	case errors.Is(err, sources.ErrBadURL):
		return "Das sieht nicht nach einem Repository-Link aus: " + err.Error()
	case errors.Is(err, core.ErrInvalid):
		return "Ungültige Angabe: " + err.Error()
	case errors.Is(err, gitcli.ErrAuth), errors.Is(err, sources.ErrAuth):
		return "Zugriff verweigert – für private Repos ein Token hinterlegen: " + err.Error()
	case errors.Is(err, sources.ErrRateLimited):
		return "API-Limit erreicht. Mit Token sind 5000 statt 60 Anfragen pro Stunde möglich: " + err.Error()
	case errors.Is(err, gitcli.ErrUnreachable):
		return "Nicht erreichbar: " + err.Error()
	}
	return "Fehler: " + err.Error()
}

// redirect after POST; htmx follows HX-Redirect.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func withMsg(to, key, msg string) string {
	sep := "?"
	if strings.Contains(to, "?") {
		sep = "&"
	}
	return to + sep + key + "=" + url.QueryEscape(msg)
}

// ---- Markdown ----

// renderMarkdown turns repository Markdown into safe HTML. Relative links
// point into the code view of repoID, relative images to the raw view.
func (s *Server) renderMarkdown(src []byte, repoID, ref, baseDir string) template.HTML {
	var buf bytes.Buffer
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithASTTransformers(
			util.Prioritized(&linkRewriter{repoID: repoID, ref: ref, base: baseDir}, 100))),
		goldmark.WithRendererOptions(html.WithXHTML()),
	)
	if repoID == "" {
		md = s.md
	}
	if err := md.Convert(src, &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(string(src)) + "</pre>")
	}
	return template.HTML(buf.String()) //nolint:gosec // goldmark without WithUnsafe escapes raw HTML and filters dangerous URLs
}

type linkRewriter struct {
	repoID, ref, base string
}

func (t *linkRewriter) Transform(doc *ast.Document, _ gtext.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			v.Destination = []byte(t.rewrite(string(v.Destination), "blob"))
		case *ast.Image:
			v.Destination = []byte(t.rewrite(string(v.Destination), "raw"))
		}
		return ast.WalkContinue, nil
	})
}

func (t *linkRewriter) rewrite(dest, kind string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") || strings.HasPrefix(dest, "//") {
		return dest
	}
	p, frag, _ := strings.Cut(dest, "#")
	p, _, _ = strings.Cut(p, "?")
	full := path.Clean("/" + t.base + "/" + p)
	if strings.HasPrefix(p, "/") {
		full = path.Clean(p)
	}
	out := "/repo/" + t.repoID + "/" + kind + full + "?ref=" + url.QueryEscape(t.ref)
	if frag != "" {
		out += "#" + frag
	}
	return out
}
