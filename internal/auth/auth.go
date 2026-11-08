// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth manages access tokens for Git HTTP, the REST API, MCP and the
// web UI (KONZEPT 6.13/6.14, T-4.3).
//
// Tokens are random strings shown once at creation. Only their SHA-256 hash
// is stored (in <data>/.reposhelf/tokens.yaml), so the data folder never
// holds a usable secret (AGENTS.md security). Each token has rights:
//
//	read   clone, fetch, browse, MCP read tools
//	write  push to own projects and forks, organise (tags, notes, relations),
//	       add and sync repositories
//	build  start builds
//	admin  everything in the web UI, including trash, retention and tokens
//
// Deleting, the trash and retention rules are never reachable over MCP,
// whatever rights a token has (D-011); the MCP layer enforces that.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Rights.
const (
	Read  = "read"
	Write = "write"
	Build = "build"
	Admin = "admin"
)

// AllRights in display order.
var AllRights = []string{Read, Write, Build, Admin}

// TokenPrefix marks RepoShelf tokens (and lets logx redact them).
const TokenPrefix = "rs_"

// EnvAdminToken lets Docker set an admin token without the UI.
const EnvAdminToken = "REPOSHELF_ADMIN_TOKEN"

// Errors.
var (
	ErrUnknown = errors.New("unknown or revoked token")
	ErrInvalid = errors.New("invalid token request")
)

// Token is a stored token (without the secret).
type Token struct {
	ID       string     `yaml:"id"`
	Name     string     `yaml:"name"`
	Hash     string     `yaml:"hash"`
	Rights   []string   `yaml:"rights,flow"`
	Created  time.Time  `yaml:"created"`
	LastUsed *time.Time `yaml:"last_used"`
}

// Has reports whether the token grants right r (admin implies all).
func (t Token) Has(r string) bool {
	return slices.Contains(t.Rights, Admin) || slices.Contains(t.Rights, r) ||
		(r == Read && len(t.Rights) > 0)
}

type file struct {
	Tokens []Token `yaml:"tokens"`
}

// Store keeps tokens in a YAML file.
type Store struct {
	mu        sync.Mutex
	path      string
	adminHash string
	now       func() time.Time
	cache     *repometa.File[file]
	lastSave  time.Time
}

// Open loads the token file at path. adminToken (may be empty) is accepted
// as an admin token without being stored.
func Open(path, adminToken string, now func() time.Time) (*Store, error) {
	s := &Store{path: path, now: now}
	if adminToken != "" {
		s.adminHash = hash(adminToken)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.cache = repometa.NewFile(file{})
		return nil
	}
	if err != nil {
		return err
	}
	f, err := repometa.ParseFile(raw, file{})
	if err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	s.cache = f
	return nil
}

func (s *Store) save() error {
	b, err := s.cache.Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.path, b, 0o600)
}

func hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Create makes a new token and returns its secret (shown once).
func (s *Store) Create(name string, rights []string) (Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Token{}, "", fmt.Errorf("%w: name missing", ErrInvalid)
	}
	if len(rights) == 0 {
		return Token{}, "", fmt.Errorf("%w: no rights", ErrInvalid)
	}
	for _, r := range rights {
		if !slices.Contains(AllRights, r) {
			return Token{}, "", fmt.Errorf("%w: unknown right %q", ErrInvalid, r)
		}
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Token{}, "", err
	}
	secret := TokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
	t := Token{ID: repometa.NewID(s.now())[16:], Name: name, Hash: hash(secret), Rights: rights, Created: s.now().UTC().Truncate(time.Second)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache.Value.Tokens = append(s.cache.Value.Tokens, t)
	if err := s.save(); err != nil {
		return Token{}, "", err
	}
	return t, secret, nil
}

// List returns all tokens, newest first.
func (s *Store) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.cache.Value.Tokens)
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Revoke removes a token by ID.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.cache.Value.Tokens)
	s.cache.Value.Tokens = slices.DeleteFunc(s.cache.Value.Tokens, func(t Token) bool { return t.ID == id })
	if len(s.cache.Value.Tokens) == before {
		return ErrUnknown
	}
	return s.save()
}

// Check validates a secret and returns its token.
func (s *Store) Check(secret string) (Token, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Token{}, ErrUnknown
	}
	h := hash(secret)
	if s.adminHash != "" && subtle.ConstantTimeCompare([]byte(h), []byte(s.adminHash)) == 1 {
		return Token{ID: "env", Name: "Admin (Umgebung)", Rights: []string{Admin}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.cache.Value.Tokens {
		if subtle.ConstantTimeCompare([]byte(h), []byte(t.Hash)) == 1 {
			now := s.now().UTC().Truncate(time.Minute)
			if t.LastUsed == nil || now.Sub(*t.LastUsed) >= time.Hour {
				s.cache.Value.Tokens[i].LastUsed = &now
				_ = s.save() // best effort; usage time is informational
			}
			return s.cache.Value.Tokens[i], nil
		}
	}
	return Token{}, ErrUnknown
}

// Local is the identity of trusted local requests (desktop, CLI on the
// same machine).
var Local = Token{ID: "local", Name: "Lokal", Rights: []string{Admin}}

// Anonymous is the identity of unauthenticated readers when allowed.
var Anonymous = Token{ID: "anonymous", Name: "Anonym", Rights: []string{Read}}
