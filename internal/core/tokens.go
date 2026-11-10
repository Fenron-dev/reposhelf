// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// HostToken is the token state of one host (T-9.10). The token itself is
// never part of it.
type HostToken struct {
	Host     string
	Provider string
	Source   sources.TokenSource
	Repos    int // repositories from this host in the library
	// Shadowed is set when the keychain holds a token that an environment
	// variable overrides.
	Shadowed bool
}

// TokenOrigin describes where a token comes from, in German.
func TokenOrigin(s sources.TokenSource) string {
	switch s.Kind {
	case sources.FromKeychain:
		return "aus dem Schlüsselbund"
	case sources.FromNone:
		return "(keiner)"
	}
	return "aus der Umgebungsvariable " + s.Name
}

// TokenHosts lists github.com and every host the library has repositories
// from, with where their token comes from.
func (l *Library) TokenHosts(ctx context.Context) ([]HostToken, error) {
	entries, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		return nil, err
	}
	count := map[string]int{"github.com": 0}
	for _, e := range entries {
		if e.Host != "" && e.Host != repometa.LocalHost {
			count[strings.ToLower(e.Host)]++
		}
	}
	out := make([]HostToken, 0, len(count))
	for h, n := range count {
		src := l.Tokens.Source(h)
		out = append(out, HostToken{Host: h, Provider: l.Sources.Provider(h), Source: src, Repos: n,
			Shadowed: src.Kind != sources.FromKeychain && src.Kind != sources.FromNone && l.Tokens.InKeychain(h)})
	}
	sort.Slice(out, func(a, b int) bool {
		if (out[a].Host == "github.com") != (out[b].Host == "github.com") {
			return out[a].Host == "github.com"
		}
		return out[a].Host < out[b].Host
	})
	return out, nil
}

// SaveToken checks token against host and stores it in the system
// keychain. A token the host rejects is not stored; when the host cannot
// be reached, the token is stored unchecked.
func (l *Library) SaveToken(ctx context.Context, host, token, actor string) (sources.Account, error) {
	host, token = strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(token)
	if !l.Tokens.CanSave() {
		return sources.Account{}, sources.ErrNoKeychain
	}
	if !sources.ValidToken(token) {
		return sources.Account{}, sources.ErrInvalidToken
	}
	acc, err := l.Sources.Check(ctx, host, token)
	if err != nil && !errors.Is(err, sources.ErrUnavailable) {
		return acc, err
	}
	if err := l.Tokens.Save(host, token); err != nil {
		return acc, err
	}
	l.Sources.Reset(host)
	l.Record(Activity{Actor: actor, Action: "token-saved", Detail: host})
	return acc, nil
}

// RemoveToken deletes host's token from the system keychain.
func (l *Library) RemoveToken(host, actor string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if err := l.Tokens.Remove(host); err != nil {
		return err
	}
	l.Sources.Reset(host)
	l.Record(Activity{Actor: actor, Action: "token-removed", Detail: host})
	return nil
}

// CheckToken checks the token currently in effect for host.
func (l *Library) CheckToken(ctx context.Context, host string) (sources.Account, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	tok := l.Tokens.Token(host)
	if tok == "" {
		return sources.Account{}, errors.New("kein Token gesetzt")
	}
	return l.Sources.Check(ctx, host, tok)
}
