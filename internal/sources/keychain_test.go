// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// fakeKeychain keeps tokens in memory.
type fakeKeychain map[string]string

func (f fakeKeychain) Get(h string) (string, error) { return f[h], nil }
func (f fakeKeychain) Set(h, t string) error        { f[h] = t; return nil }
func (f fakeKeychain) Delete(h string) error        { delete(f, h); return nil }

func TestTokenStore(t *testing.T) {
	env := map[string]string{"REPOSHELF_TOKEN_GITEA_LOCAL_3000": "from-env-123"}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	kc := fakeKeychain{}
	s := NewTokenStore(look, kc)

	if s.Token("github.com") != "" || s.Source("github.com").Kind != FromNone {
		t.Fatal("unexpected token")
	}
	if err := s.Save("GitHub.com", "github_pat_ABC123"); err != nil {
		t.Fatal(err)
	}
	if s.Token("github.com") != "github_pat_ABC123" || s.Source("github.com").Kind != FromKeychain {
		t.Errorf("saved token not used: %q %+v", s.Token("github.com"), s.Source("github.com"))
	}
	// The environment wins over the keychain (Docker, explicit setup).
	env["GITHUB_TOKEN"] = "ghp_fromenvironment"
	if src := s.Source("github.com"); src.Kind != FromEnv || src.Name != "GITHUB_TOKEN" || s.Token("github.com") != "ghp_fromenvironment" {
		t.Errorf("env did not win: %+v", src)
	}
	delete(env, "GITHUB_TOKEN")
	if err := s.Remove("github.com"); err != nil {
		t.Fatal(err)
	}
	if s.Token("github.com") != "" {
		t.Error("removed token still returned")
	}
	if s.Source("gitea.local:3000").Name != "REPOSHELF_TOKEN_GITEA_LOCAL_3000" {
		t.Error("env source for host with port")
	}

	for _, bad := range []string{"", "short", "with space token", "quote\"token123", "line\nbreak123"} {
		if err := s.Save("github.com", bad); err == nil {
			t.Errorf("Save(%q) accepted", bad)
		}
	}
	for _, bad := range []string{"", "-a.com", "a.com; rm", "a b.com", "ex ample", "a.com:123456"} {
		if err := s.Save(bad, "github_pat_ABC123"); err == nil {
			t.Errorf("Save host %q accepted", bad)
		}
	}
	if err := NewTokenStore(look, nil).Save("github.com", "github_pat_ABC123"); err != ErrNoKeychain {
		t.Errorf("save without keychain = %v", err)
	}
}

// TestSystemKeychain writes to the real keychain; it only runs when
// REPOSHELF_KEYCHAIN_TEST=1 (manual check, smoke run).
func TestSystemKeychain(t *testing.T) {
	if os.Getenv("REPOSHELF_KEYCHAIN_TEST") != "1" {
		t.Skip("set REPOSHELF_KEYCHAIN_TEST=1")
	}
	kc := SystemKeychain()
	if kc == nil {
		t.Skip("no keychain")
	}
	const host = "selftest.reposhelf.invalid"
	if err := kc.Set(host, "test_token_123"); err != nil {
		t.Fatal(err)
	}
	defer kc.Delete(host)
	if got, err := kc.Get(host); err != nil || got != "test_token_123" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := kc.Set(host, "test_token_456"); err != nil {
		t.Fatal(err)
	}
	if got, _ := kc.Get(host); got != "test_token_456" {
		t.Errorf("update = %q", got)
	}
	if err := kc.Delete(host); err != nil {
		t.Fatal(err)
	}
	if got, _ := kc.Get(host); got != "" {
		t.Errorf("after delete = %q", got)
	}
	if err := kc.Delete(host); err != nil {
		t.Errorf("second delete = %v", err)
	}
}

func TestCheckToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghp_good_token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, workflow")
		w.Write([]byte(`{"login":"octo"}`))
	})
	mux.HandleFunc("/user/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"secret"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	reg := &Registry{BaseURLs: map[string]string{"github.com": srv.URL}}

	acc, err := reg.Check(context.Background(), "github.com", "ghp_good_token")
	if err != nil || acc.Login != "octo" || acc.Scopes != "repo, workflow" || !acc.Private || !acc.Checked {
		t.Errorf("Check = %+v, %v", acc, err)
	}
	if _, err := reg.Check(context.Background(), "github.com", "ghp_bad_token"); !errors.Is(err, ErrAuth) {
		t.Errorf("bad token = %v", err)
	}
	if acc, err := reg.Check(context.Background(), "example.org", "whatever_123"); err != nil || acc.Checked {
		t.Errorf("generic host = %+v, %v", acc, err)
	}
}
