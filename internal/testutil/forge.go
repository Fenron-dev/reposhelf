// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil provides a fake code host for tests: git repositories
// served over smart HTTP (git http-backend) and a GitHub-like JSON API, all
// on a local httptest server. No test touches the real network.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	mu        sync.Mutex
	Meta      map[string]map[string]any // "owner/name" → API JSON
	Releases  map[string][]map[string]any
	Files     map[string][]byte // asset path → content
	Gone      map[string]bool
	Artifacts map[string][]map[string]any
	// Issues per "owner/name"; written by the issue API (D-031 tests).
	Issues map[string][]*FakeIssue
	// Created and Uploaded record repositories and release files created
	// through the API (V-5 tests): "owner/name" and "owner/name/tag/file".
	Created  []string
	Uploaded map[string][]byte
}

// NewForge starts a fake host.
func NewForge(t testing.TB) *Forge {
	f := &Forge{t: t, Git: Git(t), Root: t.TempDir(), Meta: map[string]map[string]any{}, Artifacts: map[string][]map[string]any{}, Issues: map[string][]*FakeIssue{}, Uploaded: map[string][]byte{},
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
	if f.publish(w, r) {
		return
	}
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
	case len(parts) == 4 && parts[2] == "actions" && parts[3] == "artifacts":
		json.NewEncoder(w).Encode(map[string]any{"artifacts": f.Artifacts[key]})
	case len(parts) >= 3 && parts[2] == "issues":
		f.issues(w, r, key, parts[3:])
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
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.f.t.Fatal(err)
	}
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

// AddArtifact publishes a CI artifact of run runID (GitHub Actions API).
func (r *Repo) AddArtifact(runID int64, name, content, created string, expired bool) {
	f := r.f
	f.mu.Lock()
	defer f.mu.Unlock()
	path := fmt.Sprintf("%s/%s/actions/%d/%s.zip", r.Owner, r.Name, runID, name)
	f.Files[path] = []byte(content)
	key := r.Owner + "/" + r.Name
	f.Artifacts[key] = append(f.Artifacts[key], map[string]any{
		"id": runID*100 + int64(len(f.Artifacts[key])), "name": name, "size_in_bytes": len(content), "expired": expired,
		"created_at": created, "archive_download_url": f.Server.URL + "/files/" + path,
		"workflow_run": map[string]any{"id": runID, "head_branch": "main", "head_sha": "abc"},
	})
}

// FakeIssue is an issue on the fake host.
type FakeIssue struct {
	Number   int
	Title    string
	Body     string
	State    string
	Comments []string
}

// issues serves the GitHub issue endpoints (list, comments, create,
// comment, change state, labels). Called with f.mu held.
func (f *Forge) issues(w http.ResponseWriter, r *http.Request, key string, rest []string) {
	find := func(s string) *FakeIssue {
		for _, is := range f.Issues[key] {
			if strconv.Itoa(is.Number) == s {
				return is
			}
		}
		return nil
	}
	var in struct {
		Title, Body, State string
	}
	if r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	now := "2026-11-11T10:00:00Z"
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		var out []map[string]any
		for _, is := range f.Issues[key] {
			out = append(out, map[string]any{"number": is.Number, "title": is.Title, "state": is.State, "body": is.Body,
				"comments": len(is.Comments), "created_at": now, "updated_at": now, "user": map[string]string{"login": "dennis"},
				"html_url": f.URL(strings.Split(key, "/")[0], strings.Split(key, "/")[1]) + "/issues/" + strconv.Itoa(is.Number)})
		}
		json.NewEncoder(w).Encode(out)
	case len(rest) == 0 && r.Method == http.MethodPost:
		is := &FakeIssue{Number: len(f.Issues[key]) + 1, Title: in.Title, Body: in.Body, State: "open"}
		f.Issues[key] = append(f.Issues[key], is)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"number": is.Number})
	case len(rest) == 1 && r.Method == http.MethodPatch:
		is := find(rest[0])
		if is == nil {
			http.NotFound(w, r)
			return
		}
		is.State = in.State
		w.Write([]byte(`{}`))
	case len(rest) == 2 && rest[1] == "comments":
		is := find(rest[0])
		if is == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			is.Comments = append(is.Comments, in.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{}`))
			return
		}
		var out []map[string]any
		for _, c := range is.Comments {
			out = append(out, map[string]any{"body": c, "created_at": now, "user": map[string]string{"login": "dennis"}})
		}
		json.NewEncoder(w).Encode(out)
	case len(rest) == 2 && rest[1] == "labels":
		w.Write([]byte(`[]`))
	default:
		http.NotFound(w, r)
	}
}

// Login is the user of every token on the fake host.
const Login = "dennis"

// publish serves the endpoints that create things (V-5): the user, new
// repositories (empty bare repositories that accept pushes), releases and
// release files. It reports whether it handled the request.
func (f *Forge) publish(w http.ResponseWriter, r *http.Request) bool {
	p := strings.TrimPrefix(r.URL.Path, "/api")
	switch {
	case p == "/user" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]string{"login": Login})
		return true
	case (p == "/user/repos" || strings.HasPrefix(p, "/orgs/")) && r.Method == http.MethodPost:
		var in struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		owner := Login
		if strings.HasPrefix(p, "/orgs/") {
			owner = strings.TrimSuffix(strings.TrimPrefix(p, "/orgs/"), "/repos")
		}
		bare := filepath.Join(f.Root, owner, in.Name+".git")
		f.run("", "init", "--bare", "--quiet", "--initial-branch=main", bare)
		f.run(bare, "config", "http.receivepack", "true")
		f.mu.Lock()
		f.Created = append(f.Created, owner+"/"+in.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"clone_url": f.URL(owner, in.Name), "html_url": f.URL(owner, in.Name)})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(p, "/repos/"), "/")
	if r.Method != http.MethodPost || len(parts) < 3 || parts[2] != "releases" {
		return false
	}
	key := parts[0] + "/" + parts[1]
	f.mu.Lock()
	defer f.mu.Unlock()
	switch len(parts) {
	case 3: // create release
		var in struct {
			Tag string `json:"tag_name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.Releases[key] = append(f.Releases[key], map[string]any{"tag_name": in.Tag})
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": len(f.Releases[key])})
		return true
	case 5: // upload asset
		id, _ := strconv.Atoi(parts[3])
		if id < 1 || id > len(f.Releases[key]) {
			http.NotFound(w, r)
			return true
		}
		tag := f.Releases[key][id-1]["tag_name"]
		b, _ := io.ReadAll(r.Body)
		f.Uploaded[fmt.Sprintf("%s/%v/%s", key, tag, r.URL.Query().Get("name"))] = b
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{}`))
		return true
	}
	return false
}

// SetGone makes owner/name answer 404 (or not) – safe while the fake host
// serves requests.
func (f *Forge) SetGone(key string, gone bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if gone {
		f.Gone[key] = true
	} else {
		delete(f.Gone, key)
	}
}
