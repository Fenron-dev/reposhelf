// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

func TestParseURL(t *testing.T) {
	cases := []struct {
		in, url, host, owner, name, provider, web string
	}{
		{"https://github.com/Fenron-dev/Fero", "https://github.com/Fenron-dev/Fero.git", "github.com", "Fenron-dev", "Fero", KindGitHub, "https://github.com/Fenron-dev/Fero"},
		{"github.com/Fenron-dev/Fero.git", "https://github.com/Fenron-dev/Fero.git", "github.com", "Fenron-dev", "Fero", KindGitHub, "https://github.com/Fenron-dev/Fero"},
		{" https://www.github.com/a/b/tree/main/docs ", "https://github.com/a/b.git", "github.com", "a", "b", KindGitHub, "https://github.com/a/b"},
		{"git@github.com:a/b.git", "ssh://git@github.com/a/b", "github.com", "a", "b", KindGitHub, "https://github.com/a/b"},
		{"https://gitlab.com/group/sub/proj/-/tree/main", "https://gitlab.com/group/sub/proj.git", "gitlab.com", "group/sub", "proj", KindGitLab, "https://gitlab.com/group/sub/proj"},
		{"http://gitea.local:3000/me/tool", "http://gitea.local:3000/me/tool", "gitea.local:3000", "me", "tool", KindGeneric, "http://gitea.local:3000/me/tool"},
		{"ssh://git@gitea.local:2222/me/tool.git", "ssh://git@gitea.local:2222/me/tool", "gitea.local", "me", "tool", KindGeneric, ""},
		{"https://user:ghp_secret@github.com/a/b", "https://github.com/a/b.git", "github.com", "a", "b", KindGitHub, "https://github.com/a/b"},
		{"https://codeberg.org/forgejo/forgejo/releases", "https://codeberg.org/forgejo/forgejo.git", "codeberg.org", "forgejo", "forgejo", KindGitea, "https://codeberg.org/forgejo/forgejo"},
	}
	for _, c := range cases {
		p, err := ParseURL(c.in)
		if err != nil {
			t.Errorf("ParseURL(%q): %v", c.in, err)
			continue
		}
		s := p.Source
		if s.URL != c.url || s.Host != c.host || s.Owner != c.owner || s.Name != c.name || p.Provider != c.provider || p.WebURL != c.web {
			t.Errorf("ParseURL(%q) = %+v %s %s", c.in, s, p.Provider, p.WebURL)
		}
	}
	for _, bad := range []string{"", "github.com", "https://github.com/a", "file:///etc/x", "ftp://h/a/b", "https://github.com/a/CON", "https://github.com/../b", "https:///a/b"} {
		if _, err := ParseURL(bad); !errors.Is(err, ErrBadURL) {
			t.Errorf("ParseURL(%q) = %v, want ErrBadURL", bad, err)
		}
	}
}

func fakeGitHub(t *testing.T) (*httptest.Server, *int32) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/a/b", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, `{"description":"Beschreibung","topics":["go","git"],"license":{"spdx_id":"MIT"},
			"language":"Go","default_branch":"main","archived":true,"stargazers_count":5,
			"created_at":"2026-01-01T00:00:00Z","pushed_at":"2026-11-01T00:00:00Z",
			"fork":true,"parent":{"full_name":"orig/b"}}`)
	})
	mux.HandleFunc("/repos/a/b/languages", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Go":1000,"Shell":10}`)
	})
	var srvURL string
	mux.HandleFunc("/repos/a/b/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[{"tag_name":"v0.1","assets":[]}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/a/b/releases?page=2>; rel="next", <x>; rel="last"`, srvURL))
		fmt.Fprintf(w, `[{"tag_name":"v1.0","name":"Eins","body":"Text","published_at":"2026-10-01T00:00:00Z",
			"assets":[{"name":"x.zip","size":3,"browser_download_url":"%s/dl/x.zip","url":"%s/repos/a/b/releases/assets/1","digest":"sha256:abc"}]}]`, srvURL, srvURL)
	})
	mux.HandleFunc("/repos/a/b/releases/assets/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Error("asset download without octet-stream accept")
		}
		w.Write([]byte("zip"))
	})
	mux.HandleFunc("/repos/a/b/issues", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"number":1,"title":"Bug","state":"open","body":"x","comments":1,"user":{"login":"u"},"labels":[{"name":"bug"}]},
			{"number":2,"title":"PR","pull_request":{}}]`)
	})
	mux.HandleFunc("/repos/a/b/issues/1/comments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"body":"same here","user":{"login":"v"}}]`)
	})
	mux.HandleFunc("/repos/a/gone", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/repos/a/limited", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"html_url":"https://github.com/a/b"}]`)
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestGitHubAPI(t *testing.T) {
	ctx := context.Background()
	srv, calls := fakeGitHub(t)
	reg := &Registry{
		Tokens:   func(string) string { return "tok" },
		BaseURLs: map[string]string{"github.com": srv.URL},
	}
	api, err := reg.API("github.com")
	if err != nil {
		t.Fatal(err)
	}
	src := repometa.Source{Host: "github.com", Owner: "a", Name: "b", URL: "https://github.com/a/b.git"}
	m, err := api.Metadata(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != "Beschreibung" || m.License != "MIT" || !m.Archived || m.ForkParent != "github.com/orig/b" || m.Languages["Go"] != 1000 || m.PushedAt == nil {
		t.Errorf("Metadata = %+v", m)
	}
	// Second call is answered from the ETag cache (304).
	if m2, err := api.Metadata(ctx, src); err != nil || m2.Description != "Beschreibung" || atomic.LoadInt32(calls) != 2 {
		t.Errorf("cached Metadata = %+v %v (%d calls)", m2, err, *calls)
	}

	rels, err := api.Releases(ctx, src)
	if err != nil || len(rels) != 2 || rels[0].Assets[0].Digest != "sha256:abc" {
		t.Fatalf("Releases = %+v, %v", rels, err)
	}
	var buf bytes.Buffer
	if err := api.Download(ctx, rels[0].Assets[0], &buf); err != nil || buf.String() != "zip" {
		t.Errorf("Download = %q %v", buf.String(), err)
	}
	issues, err := api.Issues(ctx, src, nil)
	if err != nil || len(issues) != 1 || len(issues[0].Comments) != 1 || issues[0].Labels[0] != "bug" {
		t.Errorf("Issues = %+v, %v", issues, err)
	}
	own, err := api.ListOwn(ctx, false)
	if err != nil || len(own) != 1 {
		t.Errorf("ListOwn = %v %v", own, err)
	}

	if _, err := api.Metadata(ctx, repometa.Source{Owner: "a", Name: "gone"}); !errors.Is(err, ErrGone) {
		t.Errorf("gone = %v", err)
	}
	_, err = api.Metadata(ctx, repometa.Source{Owner: "a", Name: "limited"})
	var rl *RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("limited = %v", err)
	}
	// While limited, no request is sent at all.
	if _, err := api.Metadata(ctx, src); !errors.Is(err, ErrRateLimited) {
		t.Errorf("call during limit = %v", err)
	}

	if a := reg.GitAuth(src); a == nil || a.URLPrefix != "https://github.com/" {
		t.Errorf("GitAuth = %+v", a)
	}
	if a := reg.GitAuth(repometa.Source{Host: "github.com", URL: "ssh://git@github.com/a/b"}); a != nil {
		t.Error("ssh got token auth")
	}
	if _, err := reg.API("example.org"); !errors.Is(err, ErrNoAPI) {
		t.Errorf("generic host API = %v", err)
	}
}

func TestEnvTokens(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(file, []byte("from-file\n"), 0o600)
	env := map[string]string{
		"GITHUB_TOKEN":                      "gh",
		"REPOSHELF_TOKEN_GITEA_LOCAL_3000":  "gt",
		"REPOSHELF_TOKEN_CODEBERG_ORG_FILE": file,
	}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	tok := EnvTokens(look, false)
	if tok("github.com") != "gh" || tok("gitea.local:3000") != "gt" || tok("codeberg.org") != "from-file" || tok("x.org") != "" {
		t.Error("token lookup wrong")
	}
	if !strings.HasPrefix(nextLink(`<https://a/b?page=2>; rel="next"`), "https://a/b") {
		t.Error("nextLink")
	}
}

func TestGitHubArtifacts(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/a/b/actions/artifacts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"artifacts":[{"id":2,"name":"old","size_in_bytes":5,"expired":true,"created_at":"2026-10-01T00:00:00Z","archive_download_url":"x","workflow_run":{"id":8}}]}`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/a/b/actions/artifacts?page=2>; rel="next"`, srvURL))
		fmt.Fprint(w, `{"artifacts":[{"id":1,"name":"app","size_in_bytes":10,"expired":false,"created_at":"2026-11-01T00:00:00Z",
			"archive_download_url":"https://api/zip","workflow_run":{"id":9,"head_branch":"main","head_sha":"abc"}}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	reg := &Registry{BaseURLs: map[string]string{"github.com": srv.URL}}
	api, _ := reg.API("github.com")
	arts, err := api.Artifacts(context.Background(), repometa.Source{Owner: "a", Name: "b"})
	if err != nil || len(arts) != 2 || arts[0].RunID != 9 || arts[0].Branch != "main" || !arts[1].Expired {
		t.Fatalf("artifacts = %+v, %v", arts, err)
	}
	gt, _ := (&Registry{}).API("codeberg.org")
	if _, err := gt.Artifacts(context.Background(), repometa.Source{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("gitea artifacts = %v", err)
	}
}
