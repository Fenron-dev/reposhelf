// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/store"
)

// TestForeignPagesAreNotLocal: a loopback request only counts as the local
// user when its Host names this machine – not a page whose domain was
// rebound to 127.0.0.1 (R-011) – and the API takes no cross-site posts
// (R-012).
func TestForeignPagesAreNotLocal(t *testing.T) {
	e := setup(t)
	do := func(method, path, host string, hdr map[string]string, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, e.http.URL+path, strings.NewReader(body))
		if host != "" {
			req.Host = host
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	u, _ := url.Parse(e.http.URL)

	// Rebound domain: no trust, although the connection is loopback.
	if resp := do("GET", "/settings", "evil.example:"+u.Port(), nil, ""); resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Errorf("rebound GET /settings = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://evil.example:" + u.Port()}
	if resp := do("POST", "/access/tokens", "evil.example:"+u.Port(), form, "name=x&rights=admin"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("rebound POST /access/tokens = %d", resp.StatusCode)
	}
	if resp := do("GET", "/api/repos", "evil.example:"+u.Port(), nil, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("rebound GET /api/repos = %d", resp.StatusCode)
	}
	// The machine's own names stay trusted.
	for _, host := range []string{"localhost:" + u.Port(), "127.0.0.1:" + u.Port(), "reposhelf.localhost:" + u.Port()} {
		if resp := do("GET", "/settings", host, nil, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /settings with Host %s = %d", host, resp.StatusCode)
		}
	}

	// API: a page of another site cannot post for the local user.
	before, _ := e.lib.List(t.Context(), storeQuery())
	for name, hdr := range map[string]map[string]string{
		"text/plain body":  {"Content-Type": "text/plain"},
		"foreign origin":   {"Content-Type": "application/json", "Origin": "https://evil.example"},
		"cross-site fetch": {"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
	} {
		if resp := do("POST", "/api/repos", "", hdr, `{"url":"https://github.com/a/b"}`); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: POST /api/repos = %d", name, resp.StatusCode)
		}
	}
	if resp := do("POST", "/api/sync/"+e.repo.R().ID, "", map[string]string{"Origin": "https://evil.example"}, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site sync = %d", resp.StatusCode)
	}
	if after, _ := e.lib.List(t.Context(), storeQuery()); len(after) != len(before) {
		t.Error("a cross-site request added a repository")
	}
	// A script on this machine (no browser headers) still works.
	if resp := do("POST", "/api/sync/"+e.repo.R().ID, "", nil, ""); resp.StatusCode >= 400 {
		t.Errorf("local sync = %d", resp.StatusCode)
	}
}

func storeQuery() store.Query { return store.Query{} }
