// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil provides a fake code host for tests: git repositories
// served over smart HTTP (git http-backend) and a GitHub-like JSON API, all
// on a local httptest server. No test touches the real network.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// Git returns git isolated from the user's configuration, or skips.
func Git(t testing.TB) *gitcli.Git {
	t.Helper()
	g, err := gitcli.Find("")
	if err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	g.Env = []string{
		"HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	return g
}

// Forge is a fake code host.
type Forge struct {
	t      testing.TB
	Git    *gitcli.Git
	Root   string // bare repositories: <Root>/<owner>/<name>.git
	Server *httptest.Server
	Host   string // host:port

	mu       sync.Mutex
	Meta     map[string]map[string]any // "owner/name" → API JSON
	Releases map[string][]map[string]any
	Files    map[string][]byte // asset path → content
	Gone     map[string]bool
}

// NewForge starts a fake host.
func NewForge(t testing.TB) *Forge {
	f := &Forge{t: t, Git: Git(t), Root: t.TempDir(), Meta: map[string]map[string]any{},
		Releases: map[string][]map[string]any{}, Files: map[string][]byte{}, Gone: map[string]bool{}}
	gitPath := f.Git.Path
	backend := &cgi.Handler{
		Path: gitPath, Args: []string{"http-backend"}, Dir: f.Root,
		Env: append([]string{"GIT_PROJECT_ROOT=" + f.Root, "GIT_HTTP_EXPORT_ALL=1"}, f.Git.Env...),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", f.api)
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		b, ok := f.Files[strings.TrimPrefix(r.URL.Path, "/files/")]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		key := repoKey(r.URL.Path)
		f.mu.Lock()
		gone := f.Gone[key]
		f.mu.Unlock()
		if gone {
			http.NotFound(w, r)
			return
		}
		backend.ServeHTTP(w, r)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)
	u, _ := url.Parse(f.Server.URL)
	f.Host = u.Host
	return f
}

func repoKey(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
}

// URL is the web link of a repository on the forge.
func (f *Forge) URL(owner, name string) string {
	return f.Server.URL + "/" + owner + "/" + name
}

// APIBase is the API root to configure in sources.Registry.
func (f *Forge) APIBase() string { return f.Server.URL + "/api" }

func (f *Forge) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	key := parts[0] + "/" + parts[1]
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Gone[key] {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 2:
		m, ok := f.Meta[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(m)
	case len(parts) == 3 && parts[2] == "releases":
		json.NewEncoder(w).Encode(f.Releases[key])
	case len(parts) == 3 && parts[2] == "languages":
		w.Write([]byte(`{}`))
	case len(parts) == 3 && parts[2] == "issues":
		w.Write([]byte(`[]`))
	default:
		http.NotFound(w, r)
	}
}

// Repo is a working copy whose pushes go to the forge.
type Repo struct {
	f           *Forge
	Owner, Name string
	Work, Bare  string
}

// NewRepo creates owner/name on the forge with one commit on main.
func (f *Forge) NewRepo(owner, name string) *Repo {
	f.t.Helper()
	r := &Repo{f: f, Owner: owner, Name: name, Work: filepath.Join(f.t.TempDir(), name), Bare: filepath.Join(f.Root, owner, name+".git")}
	f.run("", "init", "--bare", "--quiet", "--initial-branch=main", r.Bare)
	f.run("", "init", "--quiet", "--initial-branch=main", r.Work)
	f.run(r.Work, "remote", "add", "origin", r.Bare)
	r.Commit("README.md", "# "+name+"\n", "init")
	r.Push()
	f.mu.Lock()
	f.Meta[owner+"/"+name] = map[string]any{"description": "Repo " + name, "topics": []string{"test", "go"},
		"license": map[string]string{"spdx_id": "MIT"}, "language": "Go", "default_branch": "main",
		"html_url": f.URL(owner, name), "created_at": "2026-01-01T00:00:00Z", "pushed_at": "2026-11-01T00:00:00Z"}
	f.mu.Unlock()
	return r
}

func (f *Forge) run(dir string, args ...string) string {
	f.t.Helper()
	out, err := f.Git.Run(context.Background(), dir, nil, args...)
	if err != nil {
		f.t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// Git runs git in the working copy.
func (r *Repo) Git(args ...string) string { return r.f.run(r.Work, args...) }

// Commit writes a file and commits it; returns the commit ID.
func (r *Repo) Commit(file, content, msg string) string {
	p := filepath.Join(r.Work, file)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.f.t.Fatal(err)
	}
	r.Git("add", file)
	r.Git("commit", "--quiet", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

// Push pushes all branches and tags.
func (r *Repo) Push() {
	r.Git("push", "--quiet", "--all", "origin")
	r.Git("push", "--quiet", "--tags", "origin")
}

// ForcePush pushes with force.
func (r *Repo) ForcePush() {
	r.Git("push", "--quiet", "--force", "--all", "origin")
	r.Git("push", "--quiet", "--force", "--tags", "origin")
}

// SetMeta changes an API field.
func (r *Repo) SetMeta(k string, v any) {
	r.f.mu.Lock()
	r.f.Meta[r.Owner+"/"+r.Name][k] = v
	r.f.mu.Unlock()
}

// AddRelease publishes a release with one asset of the given content.
func (r *Repo) AddRelease(tag, published string, assetName, content string) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	path := fmt.Sprintf("%s/%s/%s/%s", r.Owner, r.Name, tag, assetName)
	f.Files[path] = []byte(content)
	key := r.Owner + "/" + r.Name
	rel := map[string]any{"tag_name": tag, "name": "Version " + tag, "body": "Änderungen in " + tag, "published_at": published,
		"assets": []map[string]any{{"name": assetName, "size": len(content), "browser_download_url": f.Server.URL + "/files/" + path}}}
	f.Releases[key] = append([]map[string]any{rel}, f.Releases[key]...)
}

// SetGone makes a repository disappear (API and git answer 404).
func (r *Repo) SetGone(gone bool) {
	r.f.mu.Lock()
	r.f.Gone[r.Owner+"/"+r.Name] = gone
	r.f.mu.Unlock()
}

// HasGitHTTPBackend reports whether git http-backend can run.
func HasGitHTTPBackend(g *gitcli.Git) bool {
	cmd := exec.Command(g.Path, "http-backend")
	cmd.Env = []string{"REQUEST_METHOD=GET", "GIT_PROJECT_ROOT=/nonexistent", "PATH_INFO=/x/info/refs"}
	return cmd.Run() == nil || cmd.ProcessState != nil
}
