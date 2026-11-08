// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.yaml")
	now := func() time.Time { return time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC) }
	s, err := Open(path, "env-secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("", []string{Read}); !errors.Is(err, ErrInvalid) {
		t.Error("empty name accepted")
	}
	if _, _, err := s.Create("x", []string{"delete"}); !errors.Is(err, ErrInvalid) {
		t.Error("unknown right accepted")
	}
	tok, secret, err := s.Create("Claude Code", []string{Read, Build})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) < 30 {
		t.Errorf("secret = %q", secret)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), secret) {
		t.Error("secret stored in clear text")
	}
	got, err := s.Check(secret)
	if err != nil || got.ID != tok.ID || !got.Has(Build) || got.Has(Write) || got.LastUsed == nil {
		t.Errorf("Check = %+v, %v", got, err)
	}
	if _, err := s.Check("rs_wrong"); !errors.Is(err, ErrUnknown) {
		t.Error("wrong token accepted")
	}
	if a, err := s.Check("env-secret"); err != nil || !a.Has(Admin) || !a.Has(Write) {
		t.Errorf("env admin = %+v %v", a, err)
	}
	// Reload from disk.
	s2, _ := Open(path, "", now)
	if len(s2.List()) != 1 {
		t.Error("token not persisted")
	}
	if err := s2.Revoke(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Check(secret); !errors.Is(err, ErrUnknown) {
		t.Error("revoked token still valid")
	}
	if err := s2.Revoke("nope"); !errors.Is(err, ErrUnknown) {
		t.Error("revoke unknown")
	}
	if !Anonymous.Has(Read) || Anonymous.Has(Write) || !Local.Has(Write) {
		t.Error("builtin identities wrong")
	}
}
