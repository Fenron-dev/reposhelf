// SPDX-License-Identifier: GPL-3.0-or-later

package githttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/cgi"
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
	push := tail == "/git-receive-pack" || r.URL.Query().Get("service") == "git-receive-pack"
	if !push && tail != "/git-upload-pack" && tail != "/info/refs" && !strings.HasPrefix(tail, "/objects/") && tail != "/HEAD" {
		http.NotFound(w, r)
		return
	}
	id, authed := h.Identity(r)
	need := auth.Read
	if push {
		need = auth.Write
	}
	if !id.Has(need) {
		if !authed {
			w.Header().Set("WWW-Authenticate", `Basic realm="RepoShelf"`)
			http.Error(w, "Anmeldung nötig: Token als Passwort angeben", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Dieses Token hat dafür keine Berechtigung", http.StatusForbidden)
		return
	}
	if push && repo.R().Kind == repometa.KindMirror {
		http.Error(w, "Spiegel sind schreibgeschützt. Lege mit „Fork“ eine beschreibbare Kopie an.", http.StatusForbidden)
		return
	}

	// Rewrite to the stored folder name (case may differ on Linux).
	r2 := r.Clone(r.Context())
	r2.URL.Path = Prefix + repo.Entry.Key + layout.MirrorSuffix + tail
	user := id.Name
	if user == "" {
		user = "reposhelf"
	}
	backend := &cgi.Handler{
		Path: h.Lib.Git.Path,
		Root: strings.TrimSuffix(Prefix, "/"),
		Args: []string{"http-backend"},
		Env: append([]string{
			"GIT_PROJECT_ROOT=" + h.Lib.Layout.Path(layout.DirRepos),
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=" + user,
		}, h.Lib.Git.Env...),
		Logger: nil,
	}
	if push {
		backend.Env = append(backend.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=true")
	} else {
		backend.Env = append(backend.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.receivepack", "GIT_CONFIG_VALUE_0=false")
	}
	backend.ServeHTTP(w, r2)

	if push && tail == "/git-receive-pack" && r.Method == http.MethodPost {
		// Statistics and index after the push; detached from the request.
		go h.Lib.RefreshAfterPush(context.WithoutCancel(r.Context()), repo.R().ID, "git:"+user)
	}
}

// CloneURL returns the clone address of a repository for base (scheme://host).
func CloneURL(base, key string) string {
	return strings.TrimSuffix(base, "/") + Prefix + key + layout.MirrorSuffix
}
