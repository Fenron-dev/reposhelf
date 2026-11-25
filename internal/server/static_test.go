// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"regexp"
	"testing"
)

// TestStaticCache: pages link static files with the content version, so
// an update reaches the browser at once; unversioned addresses are
// revalidated (R-031).
func TestStaticCache(t *testing.T) {
	e := setup(t)
	page := e.get("/", 200)
	m := regexp.MustCompile(`href="/static/app\.css\?v=([0-9a-f]{12})"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no versioned app.css in page:\n%s", page)
	}
	head := func(path, etag string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, e.http.URL+path, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := head("/static/app.css?v="+m[1], ""); r.StatusCode != 200 || r.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("versioned: %d %q", r.StatusCode, r.Header.Get("Cache-Control"))
	}
	for _, p := range []string{"/static/app.css", "/static/app.css?v=alt"} {
		r := head(p, "")
		if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q", p, r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	if r := head("/static/app.css", `"`+m[1]+`"`); r.StatusCode != http.StatusNotModified {
		t.Errorf("revalidation: %d, want 304", r.StatusCode)
	}
}
