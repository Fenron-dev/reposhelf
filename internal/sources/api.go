// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Errors of API calls.
var (
	ErrGone        = errors.New("repository not found upstream")
	ErrAuth        = errors.New("access denied upstream")
	ErrRateLimited = errors.New("API rate limit reached")
	ErrUnavailable = errors.New("upstream temporarily unavailable")
	ErrNoAPI       = errors.New("host has no supported API")
)

// RateLimitError says when the limit resets.
type RateLimitError struct{ Reset time.Time }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("API rate limit reached until %s", e.Reset.Format(time.RFC3339))
}
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// Metadata is a snapshot of what the host says about a repository
// (KONZEPT 6.3, upstream.json).
type Metadata struct {
	Description   string           `json:"description"`
	Homepage      string           `json:"homepage"`
	Topics        []string         `json:"topics"`
	License       string           `json:"license"` // SPDX, "" if none detected
	Language      string           `json:"language"`
	Languages     map[string]int64 `json:"languages,omitempty"`
	DefaultBranch string           `json:"default_branch"`
	Archived      bool             `json:"archived"`
	Private       bool             `json:"private"`
	Fork          bool             `json:"fork"`
	ForkParent    string           `json:"fork_parent,omitempty"` // host/owner/name
	HasWiki       bool             `json:"has_wiki"`
	HasIssues     bool             `json:"has_issues"`
	Stars         int              `json:"stars"`
	CreatedAt     *time.Time       `json:"created_at,omitempty"`
	PushedAt      *time.Time       `json:"pushed_at,omitempty"`
	// Size is the total size of the files of the default revision, if the
	// host reports it (Hugging Face models, T-9.11).
	Size int64 `json:"size,omitempty"`
}

// Release is a version with description and files.
type Release struct {
	Tag        string     `json:"tag"`
	Name       string     `json:"name"`
	Body       string     `json:"body"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	Published  *time.Time `json:"published,omitempty"`
	Assets     []Asset    `json:"assets"`
}

// Asset is a file attached to a release.
type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
	URL         string `json:"url"`
	// Digest as given by the host ("sha256:…"), may be empty.
	Digest string `json:"digest,omitempty"`
}

// Issue is an issue with its comments (T-9.1).
type Issue struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Author    string     `json:"author"`
	Labels    []string   `json:"labels"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	Comments  []Comment  `json:"comments"`
	URL       string     `json:"url"`
}

// Comment on an issue.
type Comment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// API reads from a hosting service.
type API interface {
	Metadata(ctx context.Context, src repometa.Source) (Metadata, error)
	Releases(ctx context.Context, src repometa.Source) ([]Release, error)
	Issues(ctx context.Context, src repometa.Source, since *time.Time) ([]Issue, error)
	Download(ctx context.Context, a Asset, w io.Writer) error
	// ListOwn lists repositories of the token owner (starred: starred ones),
	// for the mass import (T-9.6).
	ListOwn(ctx context.Context, starred bool) ([]string, error)
	// Artifacts lists build artifacts of CI runs, newest first
	// (GitHub Actions; other hosts return ErrUnsupported).
	Artifacts(ctx context.Context, src repometa.Source) ([]WorkflowArtifact, error)
}

// WorkflowArtifact is a file produced by a CI run (GitHub Actions).
type WorkflowArtifact struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
	Expired bool      `json:"expired"`
	URL     string    `json:"url"` // ZIP download (needs a token)
	RunID   int64     `json:"run_id"`
	Branch  string    `json:"branch"`
	Commit  string    `json:"commit"`
}

// ErrUnsupported is returned for features a host does not offer.
var ErrUnsupported = errors.New("not supported by this host")

// Tokens returns the token for a host ("" if none). Tokens come from
// environment variables or secret files, never from YAML (KONZEPT 4.2).
type Tokens func(host string) string

// Registry picks the API and git credentials for a host.
type Registry struct {
	HTTP   *http.Client
	Tokens Tokens
	// BaseURLs overrides API roots per host (tests, GitHub Enterprise).
	BaseURLs map[string]string
	// Kinds overrides the provider of a host (self-hosted Gitea/GitLab).
	Kinds map[string]string

	mu   sync.Mutex
	apis map[string]API
}

// Provider returns the provider kind of host.
func (r *Registry) Provider(host string) string {
	if k, ok := r.Kinds[host]; ok {
		return k
	}
	if k, ok := knownHosts[host]; ok {
		return k
	}
	return KindGeneric
}

// API returns the API client for host, or ErrNoAPI.
func (r *Registry) API(host string) (API, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a, ok := r.apis[host]; ok {
		return a, nil
	}
	c := &client{http: r.HTTP, token: r.token(host), etags: map[string]cached{}}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}
	var a API
	switch r.Provider(host) {
	case KindGitHub:
		c.base = r.base(host, "https://api.github.com")
		c.authHeader = "Bearer "
		a = &github{c: c, host: host}
	case KindGitea:
		c.base = r.base(host, "https://"+host+"/api/v1")
		c.authHeader = "token "
		a = &gitea{c: c, host: host}
	case KindGitLab:
		c.base = r.base(host, "https://"+host+"/api/v4")
		c.authHeader = "Bearer "
		a = &gitlab{c: c, host: host}
	case KindHuggingFace:
		c.base = r.base(host, "https://"+host+"/api")
		c.authHeader = "Bearer "
		a = &huggingface{c: c, host: host}
	default:
		return nil, ErrNoAPI
	}
	if r.apis == nil {
		r.apis = map[string]API{}
	}
	r.apis[host] = a
	return a, nil
}

func (r *Registry) base(host, def string) string {
	if b, ok := r.BaseURLs[host]; ok {
		return strings.TrimSuffix(b, "/")
	}
	return def
}

func (r *Registry) token(host string) string {
	if r.Tokens == nil {
		return ""
	}
	return r.Tokens(host)
}

// GitAuth returns credentials for cloning src over HTTP(S), or nil.
func (r *Registry) GitAuth(src repometa.Source) *gitcli.Auth {
	tok := r.token(src.Host)
	u, err := url.Parse(src.URL)
	if tok == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	return &gitcli.Auth{Token: tok, URLPrefix: u.Scheme + "://" + u.Host + "/"}
}

// client is a small JSON HTTP client with ETag caching: a 304 answer does
// not count against GitHub's rate limit.
type client struct {
	http       *http.Client
	base       string
	token      string
	authHeader string

	mu    sync.Mutex
	etags map[string]cached
	reset time.Time // rate limit blocked until
}

type cached struct {
	etag string
	body []byte
	next string
}

// get fetches base+path into v and returns the next page URL (Link header).
func (c *client) get(ctx context.Context, pathOrURL string, v any) (string, error) {
	u := pathOrURL
	if !strings.HasPrefix(u, "http") {
		u = c.base + pathOrURL
	}
	c.mu.Lock()
	if time.Now().Before(c.reset) {
		reset := c.reset
		c.mu.Unlock()
		return "", &RateLimitError{Reset: reset}
	}
	prev, hasPrev := c.etags[u]
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RepoShelf")
	if c.token != "" {
		req.Header.Set("Authorization", c.authHeader+c.token)
	}
	if hasPrev {
		req.Header.Set("If-None-Match", prev.etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotModified && hasPrev:
		body = prev.body
		return prev.next, json.Unmarshal(body, v)
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return "", fmt.Errorf("%s: %w", u, ErrGone)
	case resp.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("%s: %w", u, ErrAuth)
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
			reset := parseReset(resp.Header)
			c.mu.Lock()
			c.reset = reset
			c.mu.Unlock()
			return "", &RateLimitError{Reset: reset}
		}
		return "", fmt.Errorf("%s: %w", u, ErrAuth)
	case resp.StatusCode >= 500:
		return "", fmt.Errorf("%s: HTTP %d: %w", u, resp.StatusCode, ErrUnavailable)
	default:
		return "", fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	next := nextLink(resp.Header.Get("Link"))
	if et := resp.Header.Get("ETag"); et != "" {
		c.mu.Lock()
		c.etags[u] = cached{etag: et, body: body, next: next}
		c.mu.Unlock()
	}
	if err := json.Unmarshal(body, v); err != nil {
		return "", fmt.Errorf("decode %s: %w", u, err)
	}
	return next, nil
}

// download streams a URL to w (release assets; redirects are followed).
func (c *client) download(ctx context.Context, u, accept string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "RepoShelf")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.token != "" && sameHost(u, c.base) {
		req.Header.Set("Authorization", c.authHeader+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", u, resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func sameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Host == ub.Host
}

func parseReset(h http.Header) time.Time {
	if s := h.Get("X-RateLimit-Reset"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(n, 0)
		}
	}
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	return time.Now().Add(time.Hour)
}

// nextLink extracts rel="next" from a Link header.
func nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 {
			continue
		}
		for _, p := range seg[1:] {
			if strings.TrimSpace(p) == `rel="next"` {
				return strings.Trim(strings.TrimSpace(seg[0]), "<>")
			}
		}
	}
	return ""
}

// maxPages bounds pagination against misbehaving servers.
const maxPages = 50

func getAll[T any](ctx context.Context, c *client, path string) ([]T, error) {
	var all []T
	next := path
	for i := 0; next != "" && i < maxPages; i++ {
		var page []T
		n, err := c.get(ctx, next, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = n
	}
	return all, nil
}

func ptrTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
