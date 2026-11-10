// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Account describes what a token gives access to (T-9.10).
type Account struct {
	Login string
	// Scopes of a classic GitHub token ("repo, workflow"); empty for
	// fine-grained tokens, whose rights GitHub does not report.
	Scopes string
	// FineGrained is true for GitHub's fine-grained tokens.
	FineGrained bool
	// Private reports whether at least one private repository is visible.
	Private bool
	// Checked is false for hosts without a known API.
	Checked bool
}

// Reset forgets the API client of host, so a changed token is used.
func (r *Registry) Reset(host string) {
	r.mu.Lock()
	delete(r.apis, host)
	r.mu.Unlock()
}

// Check asks host who token belongs to. Hosts without a known API are not
// checked (Account.Checked false).
func (r *Registry) Check(ctx context.Context, host, token string) (Account, error) {
	hc := r.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	get := func(url, auth string, v any) (http.Header, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "RepoShelf")
		req.Header.Set("Authorization", auth+token)
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, ErrAuth
		case resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("%w: keine Berechtigung", ErrAuth)
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return resp.Header, json.Unmarshal(body, v)
	}
	var acc Account
	switch r.Provider(host) {
	case KindGitHub:
		base := r.base(host, "https://api.github.com")
		var u struct {
			Login string `json:"login"`
		}
		h, err := get(base+"/user", "Bearer ", &u)
		if err != nil {
			return acc, err
		}
		acc.Login, acc.Checked = u.Login, true
		acc.Scopes = strings.TrimSpace(h.Get("X-OAuth-Scopes"))
		acc.FineGrained = strings.HasPrefix(token, "github_pat_")
		var repos []struct{}
		if _, err := get(base+"/user/repos?visibility=private&per_page=1", "Bearer ", &repos); err == nil {
			acc.Private = len(repos) > 0
		}
	case KindGitea:
		var u struct {
			Login string `json:"login"`
		}
		if _, err := get(r.base(host, "https://"+host+"/api/v1")+"/user", "token ", &u); err != nil {
			return acc, err
		}
		acc.Login, acc.Checked = u.Login, true
	case KindGitLab:
		var u struct {
			Username string `json:"username"`
		}
		if _, err := get(r.base(host, "https://"+host+"/api/v4")+"/user", "Bearer ", &u); err != nil {
			return acc, err
		}
		acc.Login, acc.Checked = u.Username, true
	}
	return acc, nil
}
