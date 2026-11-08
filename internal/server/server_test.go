// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

type env struct {
	t     *testing.T
	f     *testutil.Forge
	lib   *core.Library
	srv   *Server
	http  *httptest.Server
	repo  *core.Repo
	other *core.Repo
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("fenron", "fero")
	up.Commit("docs/guide.md", "# Anleitung\n\n[zurück](../README.md) ![bild](img.png)\n", "docs")
	up.Commit("main.go", "package main\n\nfunc main() {}\n", "code")
	up.Git("tag", "v1.0")
	up.Push()
	up.AddRelease("v1.0", "2026-10-01T00:00:00Z", "fero.zip", "ZIP")
	f.NewRepo("fenron", "fundus")
	lib, err := core.Open(ctx, core.Options{DataDir: t.TempDir(), Git: f.Git, NoKeychain: true,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceKinds:    map[string]string{f.Host: sources.KindGitHub},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	r, err := lib.Add(ctx, core.AddRequest{URL: f.URL("fenron", "fero"), Tags: []string{"medien/tool"}, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	o, err := lib.Add(ctx, core.AddRequest{URL: f.URL("fenron", "fundus"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(lib, Options{LookupEnv: func(k string) (string, bool) {
		if k == auth.EnvAdminToken {
			return "admin-secret", true
		}
		return "", false
	}})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &env{t: t, f: f, lib: lib, srv: srv, http: hs, repo: r, other: o}
}

// get fetches a page as the trusted local user (httptest uses 127.0.0.1).
func (e *env) get(path string, want int) string {
	e.t.Helper()
	resp, err := http.Get(e.http.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("GET %s = %d, want %d\n%s", path, resp.StatusCode, want, b)
	}
	if strings.Contains(string(b), "Darstellungsfehler") {
		e.t.Fatalf("GET %s: template error\n%s", path, b)
	}
	return string(b)
}

func (e *env) post(path string, form url.Values, want int) *http.Response {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.http.URL)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("POST %s = %d, want %d\n%s", path, resp.StatusCode, want, b)
	}
	resp.Body.Close()
	return resp
}

func TestPages(t *testing.T) {
	e := setup(t)
	id := e.repo.R().ID
	body := e.get("/", 200)
	for _, want := range []string{"Bibliothek", "fero", "fundus", "medien/tool", "Aktuell"} {
		if !strings.Contains(body, want) {
			t.Errorf("library misses %q", want)
		}
	}
	for _, p := range []string{
		"/?view=tiles", "/?view=folders", "/?q=fero", "/?tag=medien/*", "/?kind=own", "/?status=gone",
		"/repo/" + id, "/repo/" + id + "?tab=versions", "/repo/" + id + "?tab=notes", "/repo/" + id + "?tab=yaml",
		"/repo/" + id + "?tab=activity", "/repo/" + id + "?tab=settings", "/repo/" + id + "?tab=issues",
		"/repo/" + id + "/tree/", "/repo/" + id + "/tree/docs?ref=v1.0", "/repo/" + id + "/blob/main.go",
		"/repo/" + id + "/blob/docs/guide.md", "/add", "/add?url=" + url.QueryEscape(e.f.URL("x", "y")),
		"/add/preview?url=" + url.QueryEscape(e.f.URL("fenron", "fero")), "/tags", "/graph", "/graph.json",
		"/activity", "/notes", "/notes/Neu", "/code", "/code?q=func+main", "/code?q=a(&regex=1", "/cleanup", "/trash", "/settings", "/access", "/collections/new", "/collections/new?smart=1",
		"/search?q=fer", "/jobs", "/login", "/api/repos", "/api/repos/" + id, "/api/tags", "/api/health",
	} {
		e.get(p, 200)
	}
	e.get("/repo/nope", 404)

	// README links point into the code view.
	guide := e.get("/repo/"+id+"/blob/docs/guide.md", 200)
	if !strings.Contains(guide, "/repo/"+id+"/blob/README.md") || !strings.Contains(guide, "/repo/"+id+"/raw/docs/img.png") {
		t.Errorf("relative links not rewritten:\n%s", guide)
	}
	if raw := e.get("/repo/"+id+"/raw/main.go", 200); !strings.Contains(raw, "func main") {
		t.Error("raw view")
	}
	asset := e.get("/repo/"+id+"/asset?tag=v1.0&name=fero.zip", 200)
	if asset != "ZIP" {
		t.Errorf("asset = %q", asset)
	}
}

func TestForms(t *testing.T) {
	e := setup(t)
	id := e.repo.R().ID
	e.post("/repo/"+id+"/tags", url.Values{"tags": {"medien/tool, rust"}}, 303)
	e.post("/repo/"+id+"/notes", url.Values{"notes": {"Siehe [[fundus]]."}}, 303)
	r, _ := e.lib.Get(context.Background(), id)
	if len(r.R().Tags) != 2 || len(r.R().Relations) == 0 {
		t.Errorf("after forms: tags %v relations %v", r.R().Tags, r.R().Relations)
	}
	e.post("/repo/"+id+"/relation", url.Values{"type": {"delivers_to"}, "target": {e.other.Entry.Key}}, 303)
	if body := e.get("/repo/"+e.other.R().ID, 200); !strings.Contains(body, "bekommt von") {
		t.Error("backlink not shown")
	}
	e.post("/repo/"+id+"/settings", url.Values{"kind": {"mirror"}, "mode": {"follow"}, "interval": {"12h"}, "releases": {"on"}, "releases_keep": {"0"}}, 303)
	e.post("/collections/_new", url.Values{"name": {"Fundus Ökosystem"}, "color": {"#44aa99"}}, 303)
	e.post("/repo/"+id+"/collections", url.Values{"collection": {"fundus-oekosystem"}}, 303)
	e.get("/collections/fundus-oekosystem", 200)
	e.post("/collections/_new", url.Values{"name": {"Medien"}, "smart": {"1"}, "logic": {"and"}, "field": {"tag", ""}, "op": {"contains", "equals"}, "value": {"medien/*", ""}}, 303)
	if body := e.get("/collections/medien", 200); !strings.Contains(body, "fero") {
		t.Error("smart collection empty")
	}
	e.post("/tags/rename", url.Values{"from": {"medien"}, "to": {"media"}}, 303)
	e.post("/repo/"+id+"/sync", nil, 303)
	e.post("/repo/"+id+"/pin", url.Values{"version": {"v1.0"}}, 303)
	e.post("/repo/"+id+"/fork", url.Values{"name": {"fero-weiter"}}, 303)
	e.post("/create", url.Values{"name": {"neu"}, "description": {"Testprojekt"}}, 303)
	e.post("/notes/_new", url.Values{"name": {"Medien"}, "text": {"Siehe [[fero]]"}}, 303)
	if body := e.get("/repo/"+id, 200); !strings.Contains(body, "Notiz „Medien“") {
		t.Error("note backlink missing on repo page")
	}

	// Cleanup: mark, review, trash, restore.
	e.post("/repo/"+e.other.R().ID+"/mark", nil, 303)
	if body := e.get("/", 200); !strings.Contains(body, "zur Löschung vorgemerkt") {
		t.Error("startup review banner missing")
	}
	plan, _ := e.lib.CleanupPlan(context.Background())
	e.post("/cleanup", url.Values{"item": {plan[0].ID}, "action": {"trash"}}, 303)
	tr, _ := e.lib.Trash()
	if len(tr) != 1 {
		t.Fatalf("trash = %+v", tr)
	}
	e.post("/trash/purge", url.Values{"id": {tr[0].ID}}, 400) // no confirmation
	e.post("/trash/restore", url.Values{"restore": {tr[0].ID}}, 303)
	if _, err := e.lib.Get(context.Background(), e.other.R().ID); err != nil {
		t.Errorf("restore failed: %v", err)
	}
	e.post("/settings", url.Values{"instance": {"Unraid"}, "default_interval": {"6h"}, "archived_interval": {"30d"}, "concurrency": {"2"}, "releases_keep": {"5"}, "trash_days": {"30"}, "capsules_keep": {"3"}, "builds_keep": {"10"}}, 303)
	if e.lib.Config().Instance.Name != "Unraid" {
		t.Error("settings not saved")
	}
	resp := e.post("/access/tokens", url.Values{"name": {"agent"}, "right": {"read", "write"}}, 200)
	_ = resp

	// CSRF: POST without matching origin is refused.
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+"/repo/"+id+"/tags", strings.NewReader("tags=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	if r2, _ := http.DefaultClient.Do(req); r2.StatusCode != 403 {
		t.Errorf("cross-site POST = %d", r2.StatusCode)
	}
}

func TestRemoteAccessNeedsToken(t *testing.T) {
	e := setup(t)
	// A forwarded request is not local.
	req, _ := http.NewRequest(http.MethodGet, e.http.URL+"/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ := c.Do(req)
	if resp.StatusCode != 303 || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Errorf("remote without token = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Login with the admin token from the environment.
	form := url.Values{"token": {"admin-secret"}, "next": {"/settings"}}
	req, _ = http.NewRequest(http.MethodPost, e.http.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.http.URL)
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	resp, _ = c.Do(req)
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/settings" || len(resp.Cookies()) == 0 {
		t.Fatalf("login = %d %v", resp.StatusCode, resp.Header)
	}
	req, _ = http.NewRequest(http.MethodGet, e.http.URL+"/settings", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.AddCookie(resp.Cookies()[0])
	if r2, _ := c.Do(req); r2.StatusCode != 200 {
		t.Errorf("with session = %d", r2.StatusCode)
	}
	// API with a read token cannot add.
	_, secret, _ := e.srv.Tokens.Create("reader", []string{auth.Read})
	req, _ = http.NewRequest(http.MethodPost, e.http.URL+"/api/repos", strings.NewReader(`{"url":"https://github.com/a/b"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	if r2, _ := c.Do(req); r2.StatusCode != 403 {
		t.Errorf("read token add = %d", r2.StatusCode)
	}
	// Wrong token never falls back to local trust.
	req, _ = http.NewRequest(http.MethodGet, e.http.URL+"/api/repos", nil)
	req.Header.Set("Authorization", "Bearer rs_wrong")
	if r2, _ := c.Do(req); r2.StatusCode != 401 {
		t.Errorf("wrong token = %d", r2.StatusCode)
	}
}

func TestGitHTTPAndMCPOverHTTP(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	g := e.f.Git
	dir := t.TempDir()
	// Clone a mirror.
	cloneURL := e.http.URL + "/git/" + e.repo.Entry.Key + ".git"
	if _, err := g.Run(ctx, "", nil, "clone", "--quiet", cloneURL, filepath.Join(dir, "fero")); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fero", "main.go")); err != nil {
		t.Error("clone incomplete")
	}
	// Protocol v0 (older clients) uses the "# service" advertisement.
	if _, err := g.Run(ctx, "", nil, "-c", "protocol.version=0", "clone", "--quiet", cloneURL, filepath.Join(dir, "fero-v0")); err != nil {
		t.Fatalf("clone v0: %v", err)
	}
	// Push to a mirror is refused.
	work := filepath.Join(dir, "fero")
	os.WriteFile(filepath.Join(work, "x.txt"), []byte("x"), 0o644)
	g.Run(ctx, work, nil, "add", "x.txt")
	g.Run(ctx, work, nil, "commit", "-qm", "x")
	if _, err := g.Run(ctx, work, nil, "push", "origin", "HEAD:main"); err == nil {
		t.Error("push to mirror succeeded")
	}
	// Push to a local project works.
	local, err := e.lib.CreateLocal(ctx, "", "projekt", "", nil, "t")
	if err != nil {
		t.Fatal(err)
	}
	g.Run(ctx, work, nil, "remote", "add", "shelf", e.http.URL+"/git/"+local.Entry.Key+".git")
	if _, err := g.Run(ctx, work, nil, "push", "--quiet", "shelf", "HEAD:main"); err != nil {
		t.Fatalf("push to own project: %v", err)
	}
	e.lib.RefreshAfterPush(ctx, local.R().ID, "test") // normally async after the push
	if !e.lib.Git.HasCommits(ctx, local.Paths.Mirror) {
		t.Error("pushed commits missing")
	}

	// MCP over HTTP needs a token.
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Errorf("remote MCP without token = %d", resp.StatusCode)
	}
	_, secret, _ := e.srv.Tokens.Create("claude", []string{auth.Read})
	tr := &sdk.StreamableClientTransport{Endpoint: e.http.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{secret}}}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "search_repos", Arguments: map[string]any{"query": "fero"}})
	if err != nil || res.IsError {
		t.Fatalf("MCP search: %v %+v", err, res)
	}
	tools, _ := cs.ListTools(ctx, nil)
	for _, tl := range tools.Tools {
		if tl.Name == "add_repo" {
			t.Error("read token sees add_repo")
		}
	}
}

type bearerRT struct{ tok string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	return http.DefaultTransport.RoundTrip(r)
}
