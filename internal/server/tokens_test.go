// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

type memKeychain map[string]string

func (m memKeychain) Get(h string) (string, error) { return m[h], nil }
func (m memKeychain) Set(h, t string) error        { m[h] = t; return nil }
func (m memKeychain) Delete(h string) error        { delete(m, h); return nil }

// TestTokenSettings enters, checks and removes a host token in the
// settings (T-9.10). The token must not end up in the data folder.
func TestTokenSettings(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer github_pat_GOOD123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/user":
			w.Write([]byte(`{"login":"dennis"}`))
		case "/user/repos":
			w.Write([]byte(`[{}]`))
		}
	}))
	defer api.Close()
	f := testutil.NewForge(t)
	kc := memKeychain{}
	dir := t.TempDir()
	lib, err := core.Open(context.Background(), core.Options{DataDir: dir, Git: f.Git, Keychain: kc,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceBaseURLs: map[string]string{"github.com": api.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	srv, err := New(lib, Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	e := &env{t: t, http: hs}

	// Without a token a private repository looks missing: say so.
	if pv, err := lib.Preview(context.Background(), "https://github.com/dennis/RepoShelf"); err != nil || !pv.NoToken {
		t.Errorf("preview without token: NoToken=%v, %v", pv.NoToken, err)
	}
	if _, err := lib.Add(context.Background(), core.AddRequest{URL: "https://github.com/dennis/RepoShelf"}); err == nil || !strings.Contains(err.Error(), "Einstellungen → Zugänge") {
		t.Errorf("add without token = %v", err)
	}
	if body := e.get("/settings/tokens", 200); !strings.Contains(body, "github.com") || !strings.Contains(body, "keiner") {
		t.Fatalf("token list:\n%s", body)
	}
	resp := e.post("/settings/tokens", url.Values{"action": {"save"}, "host": {"github.com"}, "token": {"github_pat_BAD456"}}, 303)
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "token_fehler=") || kc["github.com"] != "" {
		t.Errorf("rejected token saved: %s", loc)
	}
	resp = e.post("/settings/tokens", url.Values{"action": {"save"}, "host": {"github.com"}, "token": {" github_pat_GOOD123 "}}, 303)
	loc, _ := url.QueryUnescape(resp.Header.Get("Location"))
	if !strings.Contains(loc, "Angemeldet als dennis") || !strings.Contains(loc, "private Repos sichtbar") || kc["github.com"] != "github_pat_GOOD123" {
		t.Errorf("save: %s, keychain %v", loc, kc)
	}
	if lib.Sources.Tokens("github.com") != "github_pat_GOOD123" {
		t.Error("saved token not in effect")
	}
	if body := e.get("/settings/tokens", 200); !strings.Contains(body, "im Schlüsselbund") || strings.Contains(body, "GOOD123") {
		t.Errorf("token state wrong or token shown:\n%s", body)
	}
	resp = e.post("/settings/tokens", url.Values{"action": {"check"}, "host": {"github.com"}}, 303)
	if loc, _ := url.QueryUnescape(resp.Header.Get("Location")); !strings.Contains(loc, "gültig") {
		t.Errorf("check: %s", loc)
	}
	// Never in the data folder (KONZEPT 4.2).
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "GOOD123") {
				t.Errorf("token written to %s", p)
			}
		}
		return nil
	})
	e.post("/settings/tokens", url.Values{"action": {"remove"}, "host": {"github.com"}}, 303)
	if kc["github.com"] != "" || lib.Sources.Tokens("github.com") != "" {
		t.Error("token not removed")
	}
	if !lib.Tokens.CanSave() || sources.NewTokenStore(nil, nil).CanSave() {
		t.Error("CanSave")
	}
}
