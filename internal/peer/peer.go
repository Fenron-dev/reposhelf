// SPDX-License-Identifier: GPL-3.0-or-later

// Package peer pairs RepoShelf instances and syncs selected repositories
// between them (KONZEPT 6.15, Phase 8).
//
// Trust: every instance has an Ed25519 key pair (.reposhelf/instance.key,
// readable only by the owner). Pairing exchanges public keys, proven by a
// short one-time code shown on the other instance (Fundus2 pattern: code +
// fingerprint). Afterwards every request between the two is signed; no
// password of the other side is ever stored.
//
// Sync is a pull: this instance fetches the selected repositories from the
// peer – metadata field by field (conflicts are shown, never silently
// overwritten), code via git with the usual force-push protection, and on
// request releases and capsules.
package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Files below .reposhelf/.
const (
	KeyFile       = "instance.key"
	PeersFile     = "peers.yaml"
	ConflictsFile = "conflicts.yaml"
	BaseDir       = "peer-base"
)

// Sync levels (KONZEPT 6.15).
const (
	LevelMeta     = "meta"
	LevelCode     = "code"
	LevelReleases = "releases"
	LevelCapsules = "capsules"
)

// Levels in increasing order.
var Levels = []string{LevelMeta, LevelCode, LevelReleases, LevelCapsules}

// Errors.
var (
	ErrBadCode      = errors.New("pairing code wrong or expired")
	ErrUnknownPeer  = errors.New("unknown peer")
	ErrBadSignature = errors.New("invalid signature")
)

// Selection says what to take from a peer.
type Selection struct {
	Type  string `yaml:"type"`  // all, collection, repo
	Ref   string `yaml:"ref"`   // collection ID or repo ID/key
	Level string `yaml:"level"` // meta, code, releases, capsules
}

// Peer is a paired instance.
type Peer struct {
	ID          string      `yaml:"id"` // fingerprint
	Name        string      `yaml:"name"`
	URL         string      `yaml:"url"`
	PublicKey   string      `yaml:"public_key"`
	Paired      time.Time   `yaml:"paired"`
	LastSync    *time.Time  `yaml:"last_sync"`
	LastResult  string      `yaml:"last_result"`
	Selections  []Selection `yaml:"selections"`
	Interval    string      `yaml:"interval"` // "" = manual (default)
	AllowBuilds bool        `yaml:"allow_builds"`
	// Runner capabilities the peer reported.
	Capabilities []string `yaml:"capabilities,flow"`
}

type peersFile struct {
	Peers []Peer `yaml:"peers"`
}

// Manager holds the pairing state of one library.
type Manager struct {
	Lib  *core.Library
	Log  *slog.Logger
	HTTP *http.Client
	// PublicURL returns how the peer reaches this instance.
	PublicURL func() string

	priv ed25519.PrivateKey
	pub  ed25519.PublicKey

	mu      sync.Mutex
	codes   map[string]time.Time // pairing codes
	misses  int                  // wrong pairing proofs since the last code
	nonces  map[string]time.Time // nonces of signed requests seen (replay)
	temp    map[string]tempToken // short-lived git tokens for peers
	syncing map[string]bool
	builds  Builds
}

type tempToken struct {
	peer    string
	expires time.Time
}

// New loads (or creates) the instance key.
func New(lib *core.Library, log *slog.Logger) (*Manager, error) {
	m := &Manager{Lib: lib, Log: log, HTTP: &http.Client{Timeout: 2 * time.Minute},
		codes: map[string]time.Time{}, temp: map[string]tempToken{}, syncing: map[string]bool{}}
	p := lib.Layout.Path(layout.DirInternal, KeyFile)
	raw, err := os.ReadFile(p)
	switch {
	case err == nil:
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s: invalid key", p)
		}
		m.priv = ed25519.NewKeyFromSeed(seed)
	case errors.Is(err, os.ErrNotExist):
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		m.priv = priv
		if err := atomicfile.WriteFile(p, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	m.pub = m.priv.Public().(ed25519.PublicKey)
	return m, nil
}

// Fingerprint identifies a public key for humans (compare on both sides).
func Fingerprint(pub []byte) string {
	h := sha256.Sum256(pub)
	s := strings.ToUpper(hex.EncodeToString(h[:10]))
	var parts []string
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, "-")
}

// OwnFingerprint of this instance.
func (m *Manager) OwnFingerprint() string { return Fingerprint(m.pub) }

// ---- peers.yaml ----

func (m *Manager) path(name string) string { return m.Lib.Layout.Path(layout.DirInternal, name) }

// Peers lists the paired instances.
func (m *Manager) Peers() []Peer {
	raw, err := os.ReadFile(m.path(PeersFile))
	if err != nil {
		return nil
	}
	f, err := repometa.ParseFile(raw, peersFile{})
	if err != nil {
		return nil
	}
	return f.Value.Peers
}

func (m *Manager) savePeers(peers []Peer) error {
	var f *repometa.File[peersFile]
	if raw, err := os.ReadFile(m.path(PeersFile)); err == nil {
		if parsed, err := repometa.ParseFile(raw, peersFile{}); err == nil {
			f = parsed
		}
	}
	if f == nil {
		f = repometa.NewFile(peersFile{})
	}
	f.Value.Peers = peers
	b, err := f.Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.path(PeersFile), b, 0o600)
}

// Peer returns one peer by ID or name.
func (m *Manager) Peer(ref string) (Peer, error) {
	for _, p := range m.Peers() {
		if p.ID == ref || strings.EqualFold(p.Name, ref) {
			return p, nil
		}
	}
	return Peer{}, fmt.Errorf("%w: %s", ErrUnknownPeer, ref)
}

// UpdatePeer changes a stored peer.
func (m *Manager) UpdatePeer(id string, fn func(*Peer)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	peers := m.Peers()
	for i := range peers {
		if peers[i].ID == id {
			fn(&peers[i])
			return m.savePeers(peers)
		}
	}
	return ErrUnknownPeer
}

// Remove unpairs a peer (its data stays; only trust is removed).
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	peers := m.Peers()
	n := len(peers)
	peers = slices.DeleteFunc(peers, func(p Peer) bool { return p.ID == id })
	if len(peers) == n {
		return ErrUnknownPeer
	}
	return m.savePeers(peers)
}

func (m *Manager) addPeer(p Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	peers := slices.DeleteFunc(m.Peers(), func(x Peer) bool { return x.ID == p.ID })
	return m.savePeers(append(peers, p))
}

// ---- pairing ----

// codeTTL is how long a pairing code is valid.
const codeTTL = 10 * time.Minute

// codeChars is the length of a pairing code: 12 base32 characters are
// 60 bits – the proof sent while pairing (HMAC of code and key) cannot be
// tried out offline within codeTTL (R-021).
const codeChars = 12

// maxMisses wrong pairing proofs invalidate all open codes.
const maxMisses = 10

// NewCode creates a pairing code to show to the user.
func (m *Manager) NewCode() (string, time.Time) {
	var b [8]byte
	rand.Read(b[:])
	code := base32.StdEncoding.EncodeToString(b[:])[:codeChars]
	exp := time.Now().Add(codeTTL)
	m.mu.Lock()
	m.codes[code] = exp
	m.misses = 0
	m.mu.Unlock()
	return code[:4] + "-" + code[4:8] + "-" + code[8:], exp
}

func normCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(c))
}

func proof(code string, pub []byte) string {
	mac := hmac.New(sha256.New, []byte(normCode(code)))
	mac.Write(pub)
	return hex.EncodeToString(mac.Sum(nil))
}

type pairRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	PublicKey string `json:"public_key"`
	Proof     string `json:"proof"`
}

type pairResponse struct {
	Name         string   `json:"name"`
	PublicKey    string   `json:"public_key"`
	Proof        string   `json:"proof"`
	Capabilities []string `json:"capabilities"`
}

// Pair connects to the instance at url using the code shown there.
func (m *Manager) Pair(ctx context.Context, url, code string) (Peer, error) {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	req := pairRequest{Name: m.Lib.Config().Instance.Name, URL: m.PublicURL(), PublicKey: base64.StdEncoding.EncodeToString(m.pub), Proof: proof(code, m.pub)}
	var resp pairResponse
	if err := m.postJSON(ctx, url+"/peer/pair", req, &resp, false, Peer{}); err != nil {
		return Peer{}, err
	}
	pub, err := base64.StdEncoding.DecodeString(resp.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return Peer{}, ErrBadSignature
	}
	if !hmac.Equal([]byte(resp.Proof), []byte(proof(code, pub))) {
		return Peer{}, fmt.Errorf("%w: the other instance could not prove the code", ErrBadCode)
	}
	p := Peer{ID: Fingerprint(pub), Name: resp.Name, URL: url, PublicKey: resp.PublicKey, Paired: time.Now().UTC().Truncate(time.Second),
		Selections: []Selection{{Type: "all", Level: LevelCode}}, Capabilities: resp.Capabilities}
	if err := m.addPeer(p); err != nil {
		return Peer{}, err
	}
	m.Lib.Record(core.Activity{Actor: "peer", Action: "pair", Detail: p.Name + " " + p.ID})
	return p, nil
}

// acceptPair is the server side of Pair.
func (m *Manager) acceptPair(req pairRequest) (pairResponse, error) {
	pub, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return pairResponse{}, ErrBadSignature
	}
	m.mu.Lock()
	var used string
	for code, exp := range m.codes {
		if time.Now().After(exp) {
			delete(m.codes, code)
			continue
		}
		if hmac.Equal([]byte(req.Proof), []byte(proof(code, pub))) {
			used = code
		}
	}
	if used != "" {
		delete(m.codes, used) // one-time
	} else if m.misses++; m.misses >= maxMisses {
		clear(m.codes) // someone is guessing: the user makes a new code
	}
	m.mu.Unlock()
	if used == "" {
		return pairResponse{}, ErrBadCode
	}
	p := Peer{ID: Fingerprint(pub), Name: req.Name, URL: strings.TrimSuffix(req.URL, "/"), PublicKey: req.PublicKey, Paired: time.Now().UTC().Truncate(time.Second)}
	if err := m.addPeer(p); err != nil {
		return pairResponse{}, err
	}
	m.Lib.Record(core.Activity{Actor: "peer", Action: "pair", Detail: p.Name + " " + p.ID})
	cfg := m.Lib.Config()
	var caps []string
	if cfg.Runner.Enabled {
		caps = cfg.Runner.Capabilities
	}
	return pairResponse{Name: cfg.Instance.Name, PublicKey: base64.StdEncoding.EncodeToString(m.pub), Proof: proof(used, m.pub), Capabilities: caps}, nil
}

// ---- signed requests ----

const (
	hdrPeer  = "X-Reposhelf-Peer"
	hdrTime  = "X-Reposhelf-Time"
	hdrSig   = "X-Reposhelf-Signature"
	hdrNonce = "X-Reposhelf-Nonce"
	// maxSkew tolerates clock differences between instances.
	maxSkew = 5 * time.Minute
)

// The nonce makes every signed request unique: one seen before is refused,
// so a recorded request cannot be sent again within maxSkew (R-021).
func signingString(method, path, ts, nonce string, body []byte) []byte {
	h := sha256.Sum256(body)
	return []byte(method + "\n" + path + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(h[:]))
}

func (m *Manager) sign(r *http.Request, body []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	var nb [16]byte
	rand.Read(nb[:])
	nonce := hex.EncodeToString(nb[:])
	r.Header.Set(hdrPeer, m.OwnFingerprint())
	r.Header.Set(hdrTime, ts)
	r.Header.Set(hdrNonce, nonce)
	r.Header.Set(hdrSig, base64.StdEncoding.EncodeToString(ed25519.Sign(m.priv, signingString(r.Method, r.URL.RequestURI(), ts, nonce, body))))
}

// fresh records a nonce; false if it was used before.
func (m *Manager) fresh(nonce string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nonces == nil {
		m.nonces = map[string]time.Time{}
	}
	now := time.Now()
	for n, t := range m.nonces {
		if now.Sub(t) > 2*maxSkew {
			delete(m.nonces, n)
		}
	}
	if _, seen := m.nonces[nonce]; seen {
		return false
	}
	m.nonces[nonce] = now
	return true
}

// verify checks a signed request and returns the peer.
func (m *Manager) verify(r *http.Request, body []byte) (Peer, error) {
	p, err := m.Peer(r.Header.Get(hdrPeer))
	if err != nil {
		return Peer{}, err
	}
	ts := r.Header.Get(hdrTime)
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(sec, 0)).Abs() > maxSkew {
		return Peer{}, fmt.Errorf("%w: time", ErrBadSignature)
	}
	nonce := r.Header.Get(hdrNonce)
	if len(nonce) != 32 {
		return Peer{}, fmt.Errorf("%w: die andere Instanz ist zu alt – bitte beide auf mindestens v0.5.4 aktualisieren", ErrBadSignature)
	}
	pub, _ := base64.StdEncoding.DecodeString(p.PublicKey)
	sig, _ := base64.StdEncoding.DecodeString(r.Header.Get(hdrSig))
	if len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, signingString(r.Method, r.URL.RequestURI(), ts, nonce, body), sig) {
		return Peer{}, ErrBadSignature
	}
	if !m.fresh(nonce) {
		return Peer{}, fmt.Errorf("%w: wiederholte Anfrage", ErrBadSignature)
	}
	return p, nil
}

func (m *Manager) do(ctx context.Context, method, url string, body []byte, signed bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if signed {
		m.sign(req, body)
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// TempToken issues a short-lived read token for git and downloads; it is
// kept only in memory (never written to disk).
func (m *Manager) issueTemp(peer string) string {
	var b [24]byte
	rand.Read(b[:])
	tok := "rsp_" + hex.EncodeToString(b[:])
	m.mu.Lock()
	m.temp[tok] = tempToken{peer: peer, expires: time.Now().Add(time.Hour)}
	m.mu.Unlock()
	return tok
}

// CheckTemp validates a temporary peer token (used by the server's
// identity resolution for git HTTP).
func (m *Manager) CheckTemp(secret string) (auth.Token, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.temp[secret]
	if !ok || time.Now().After(t.expires) {
		delete(m.temp, secret)
		return auth.Token{}, false
	}
	name := t.peer
	if p, err := m.peerLocked(t.peer); err == nil {
		name = p.Name
	}
	return auth.Token{ID: "peer:" + t.peer, Name: "Instanz " + name, Rights: []string{auth.Read}}, true
}

func (m *Manager) peerLocked(id string) (Peer, error) {
	for _, p := range m.Peers() {
		if p.ID == id {
			return p, nil
		}
	}
	return Peer{}, ErrUnknownPeer
}

var _ = filepath.Join
