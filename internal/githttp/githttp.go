// SPDX-License-Identifier: GPL-3.0-or-later

package githttp

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Prefix is where repositories are served: /git/<host>/<owner>/<name>.git
const Prefix = "/git/"

// Identity returns who made the request (set by the server's auth
// middleware) and whether the request was authenticated at all.
type Identity func(r *http.Request) (auth.Token, bool)

// Handler serves git clone/fetch for all repositories and push for own
// projects and forks (KONZEPT 6.14).
type Handler struct {
	Lib      *core.Library
	Identity Identity
	Log      *slog.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, Prefix)
	i := strings.Index(rest, ".git/")
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	key, tail := rest[:i], rest[i+len(".git"):]
	repo, err := h.Lib.Get(r.Context(), key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(tail, "/info/lfs/") {
		h.lfs(w, r, key, repo, tail)
		return
	}
	push := tail == "/git-receive-pack" || r.URL.Query().Get("service") == "git-receive-pack"
	if !push && tail != "/git-upload-pack" && tail != "/info/refs" {
		http.NotFound(w, r)
		return
	}
	need := auth.Read
	if push {
		need = auth.Write
	}
	if !h.allowed(w, r, need) {
		return
	}
	id, _ := h.Identity(r)
	if push && repo.R().Kind == repometa.KindMirror {
		http.Error(w, "Spiegel sind schreibgeschützt. Lege mit „Fork“ eine beschreibbare Kopie an.", http.StatusForbidden)
		return
	}

	user := id.Name
	if user == "" {
		user = "reposhelf"
	}
	service := "git-upload-pack"
	if push {
		service = "git-receive-pack"
	}
	switch {
	case tail == "/info/refs" && r.Method == http.MethodGet:
		h.advertise(w, r, repo.Paths.Mirror, service)
	case (tail == "/git-upload-pack" || tail == "/git-receive-pack") && r.Method == http.MethodPost:
		h.rpc(w, r, repo.Paths.Mirror, strings.TrimPrefix(tail, "/"))
		if push {
			// Statistics and index after the push; detached from the request.
			go h.Lib.RefreshAfterPush(context.WithoutCancel(r.Context()), repo.R().ID, "git:"+user)
		}
	default:
		// The dumb protocol is not offered; every git since 1.6.6 speaks smart HTTP.
		http.Error(w, "Nur Smart-HTTP wird unterstützt", http.StatusNotFound)
	}
}

// allowed checks the right; it answers 401/403 itself when missing.
func (h *Handler) allowed(w http.ResponseWriter, r *http.Request, need string) bool {
	id, authed := h.Identity(r)
	if id.Has(need) {
		return true
	}
	if !authed {
		w.Header().Set("WWW-Authenticate", `Basic realm="RepoShelf"`)
		http.Error(w, "Anmeldung nötig: Token als Passwort angeben", http.StatusUnauthorized)
		return false
	}
	http.Error(w, "Dieses Token hat dafür keine Berechtigung", http.StatusForbidden)
	return false
}

// advertise answers GET info/refs (smart HTTP discovery). It calls
// git upload-pack / receive-pack directly instead of git http-backend,
// which minimal Git builds (Alpine, MinGit) do not ship.
func (h *Handler) advertise(w http.ResponseWriter, r *http.Request, dir, service string) {
	if r.URL.Query().Get("service") != service {
		http.Error(w, "unbekannter Dienst", http.StatusForbidden)
		return
	}
	proto := protocolHeader(r)
	cmd := h.Lib.Git.Command(r.Context(), dir, strings.TrimPrefix(service, "git-"), "--stateless-rpc", "--advertise-refs", ".")
	if proto != "" && service == "git-upload-pack" {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+proto)
	}
	out, err := cmd.Output()
	if err != nil {
		h.Log.Warn("git advertise", "dir", dir, "err", err)
		http.Error(w, "git-Fehler", http.StatusInternalServerError)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/x-"+service+"-advertisement")
	hd.Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	if !strings.Contains(proto, "version=2") || service != "git-upload-pack" {
		w.Write(pktLine("# service=" + service + "\n"))
		w.Write([]byte("0000"))
	}
	w.Write(out)
}

// rpc answers POST git-upload-pack / git-receive-pack.
func (h *Handler) rpc(w http.ResponseWriter, r *http.Request, dir, service string) {
	if r.Header.Get("Content-Type") != "application/x-"+service+"-request" {
		http.Error(w, "falscher Inhaltstyp", http.StatusUnsupportedMediaType)
		return
	}
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = gz.Close() }()
		body = gz
	}
	cmd := h.Lib.Git.Command(r.Context(), dir, strings.TrimPrefix(service, "git-"), "--stateless-rpc", ".")
	if proto := protocolHeader(r); proto != "" && service == "git-upload-pack" {
		cmd.Env = append(cmd.Env, "GIT_PROTOCOL="+proto)
	}
	cmd.Stdin = body
	var stderr strings.Builder
	cmd.Stderr = &stderr
	w.Header().Set("Content-Type", "application/x-"+service+"-result")
	w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	cmd.Stdout = flushWriter{w}
	if err := cmd.Run(); err != nil {
		h.Log.Warn("git rpc", "service", service, "dir", dir, "err", err, "stderr", strings.TrimSpace(stderr.String()))
	}
}

// protocolHeader passes only well-formed protocol requests to git.
func protocolHeader(r *http.Request) string {
	p := r.Header.Get("Git-Protocol")
	for _, c := range p {
		allowed := strings.ContainsRune("=:.-_", c) || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !allowed {
			return ""
		}
	}
	return p
}

func pktLine(s string) []byte {
	return []byte(fmt.Sprintf("%04x%s", len(s)+4, s))
}

// flushWriter streams large packs instead of buffering them.
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// CloneURL returns the clone address of a repository for base (scheme://host).
func CloneURL(base, key string) string {
	return strings.TrimSuffix(base, "/") + Prefix + key + layout.MirrorSuffix
}
