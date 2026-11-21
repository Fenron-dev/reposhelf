// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// KeychainService is the service name of RepoShelf's entries in the system
// keychain; the account is the host.
const KeychainService = "RepoShelf"

// Keychain keeps host tokens in the operating system's secret store
// (macOS Keychain, Windows Credential Manager, Linux Secret Service).
type Keychain interface {
	// Get returns the token for host, "" when there is none.
	Get(host string) (string, error)
	Set(host, token string) error
	// Delete removes the token; a missing entry is not an error.
	Delete(host string) error
}

// Where a token comes from.
const (
	FromNone     = ""
	FromEnv      = "env"
	FromFile     = "file"
	FromKeychain = "keychain"
)

// TokenSource names the origin of a host's token.
type TokenSource struct {
	Kind string // FromNone, FromEnv, FromFile, FromKeychain
	Name string // environment variable, if any
}

// ErrNoKeychain is returned when tokens cannot be saved on this system.
var ErrNoKeychain = errors.New("kein Schlüsselbund verfügbar – Token als Umgebungsvariable setzen")

// ErrInvalidToken is returned for values that cannot be a token.
var ErrInvalidToken = errors.New("das sieht nicht wie ein Token aus")

// Tokens of the common hosts consist of these characters only; anything
// else (spaces, quotes, line breaks) is a copy-and-paste mistake.
var tokenChars = regexp.MustCompile(`^[A-Za-z0-9_.~+/=-]{8,512}$`)

// ValidToken reports whether s can be a host token.
func ValidToken(s string) bool { return tokenChars.MatchString(s) }

// TokenStore looks up tokens for a host, in this order:
//
//  1. REPOSHELF_TOKEN_<HOST> (e.g. REPOSHELF_TOKEN_GITHUB_COM), or
//     REPOSHELF_TOKEN_<HOST>_FILE naming a file (Docker secrets),
//  2. for github.com: REPOSHELF_GITHUB_TOKEN, GITHUB_TOKEN, GH_TOKEN (and
//     their _FILE variants),
//  3. the system keychain under the service "RepoShelf" with the host as
//     account.
//
// Tokens are never stored in the data folder (KONZEPT 4.2, D-024).
type TokenStore struct {
	lookupEnv func(string) (string, bool)
	kc        Keychain

	mu    sync.Mutex
	cache map[string]string
}

// NewTokenStore returns a store; kc may be nil (no keychain).
func NewTokenStore(lookupEnv func(string) (string, bool), kc Keychain) *TokenStore {
	return &TokenStore{lookupEnv: lookupEnv, kc: kc, cache: map[string]string{}}
}

// EnvTokens returns the token lookup of a store with the system keychain
// (or none).
func EnvTokens(lookupEnv func(string) (string, bool), useKeychain bool) Tokens {
	var kc Keychain
	if useKeychain {
		kc = SystemKeychain()
	}
	return NewTokenStore(lookupEnv, kc).Token
}

// Token returns the token for host ("" if none).
func (s *TokenStore) Token(host string) string {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.cache[host]; ok {
		return t
	}
	t, _ := s.lookup(host)
	s.cache[host] = t
	return t
}

// Source tells where host's token comes from.
func (s *TokenStore) Source(host string) TokenSource {
	host = strings.ToLower(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	t, src := s.lookup(host)
	s.cache[host] = t
	return src
}

// InKeychain reports whether the keychain holds a token for host (it may
// be overridden by an environment variable).
func (s *TokenStore) InKeychain(host string) bool {
	if s.kc == nil {
		return false
	}
	t, err := s.kc.Get(strings.ToLower(host))
	return err == nil && t != ""
}

// CanSave reports whether tokens can be saved in a keychain here.
func (s *TokenStore) CanSave() bool { return s.kc != nil }

// Save stores token for host in the keychain. A token from an environment
// variable still wins over it.
func (s *TokenStore) Save(host, token string) error {
	host, token = strings.ToLower(strings.TrimSpace(host)), strings.TrimSpace(token)
	if s.kc == nil {
		return ErrNoKeychain
	}
	if !ValidToken(token) {
		return ErrInvalidToken
	}
	if !validHost(host) {
		return errors.New("ungültiger Host")
	}
	err := s.kc.Set(host, token)
	s.forget(host)
	return err
}

// Remove deletes host's token from the keychain.
func (s *TokenStore) Remove(host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if s.kc == nil {
		return ErrNoKeychain
	}
	if !validHost(host) {
		return errors.New("ungültiger Host")
	}
	err := s.kc.Delete(host)
	s.forget(host)
	return err
}

func (s *TokenStore) forget(host string) {
	s.mu.Lock()
	delete(s.cache, host)
	s.mu.Unlock()
}

func (s *TokenStore) lookup(host string) (string, TokenSource) {
	names := []string{"REPOSHELF_TOKEN_" + envName(host)}
	if host == "github.com" {
		names = append(names, "REPOSHELF_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN")
	}
	for _, n := range names {
		if v, ok := s.lookupEnv(n); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), TokenSource{Kind: FromEnv, Name: n}
		}
		if f, ok := s.lookupEnv(n + "_FILE"); ok && f != "" {
			if b, err := os.ReadFile(f); err == nil && strings.TrimSpace(string(b)) != "" {
				return strings.TrimSpace(string(b)), TokenSource{Kind: FromFile, Name: n + "_FILE"}
			}
		}
	}
	if s.kc != nil {
		if t, err := s.kc.Get(host); err == nil && t != "" {
			return t, TokenSource{Kind: FromKeychain}
		}
	}
	return "", TokenSource{}
}

// validHost accepts host names with an optional port; the host ends up as
// an argument of keychain tools.
var hostChars = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

func validHost(h string) bool { return len(h) <= 253 && hostChars.MatchString(h) }

// envName turns "gitea.local:3000" into "GITEA_LOCAL_3000".
func envName(host string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return '_'
	}, host)
}

// Secrets other than host tokens (e.g. a webhook address) are kept in the
// keychain under their own account name.
var secretAccount = regexp.MustCompile(`^reposhelf-[a-z0-9-]{1,40}$`)

// Secret returns a stored secret ("" if none).
func (s *TokenStore) Secret(account string) (string, error) {
	if s.kc == nil || !secretAccount.MatchString(account) {
		return "", nil
	}
	return s.kc.Get(account)
}

// SaveSecret stores a secret in the keychain.
func (s *TokenStore) SaveSecret(account, value string) error {
	if s.kc == nil {
		return ErrNoKeychain
	}
	if !secretAccount.MatchString(account) || value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("ungültiges Geheimnis")
	}
	return s.kc.Set(account, value)
}

// RemoveSecret deletes a secret from the keychain.
func (s *TokenStore) RemoveSecret(account string) error {
	if s.kc == nil || !secretAccount.MatchString(account) {
		return nil
	}
	return s.kc.Delete(account)
}
