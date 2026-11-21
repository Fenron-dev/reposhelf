// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/repo/x?tab=issues":    "/repo/x?tab=issues",
		"":                      "/",
		"//evil.example":        "/",
		"/\\evil.example":       "/",
		"https://evil.example/": "/",
		"/ok\r\nSet-Cookie: x":  "/",
		"javascript:alert(1)":   "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWrongTokensArePaused: after too many wrong tokens from one address,
// even the right one is not checked for a while (R-018); sessions are
// Secure behind an HTTPS proxy (R-020).
func TestWrongTokensArePaused(t *testing.T) {
	e := setup(t)
	try := func(secret string) int {
		req, _ := http.NewRequest(http.MethodGet, e.http.URL+"/api/repos", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := try("admin-secret"); c != http.StatusOK {
		t.Fatalf("right token = %d", c)
	}
	for i := 0; i < maxFailures; i++ {
		try("falsch")
	}
	if c := try("admin-secret"); c != http.StatusUnauthorized {
		t.Errorf("right token during the pause = %d", c)
	}

	// Login: a fresh server (no pause), behind an HTTPS proxy.
	e2 := setup(t)
	req, _ := http.NewRequest(http.MethodPost, e2.http.URL+"/login", strings.NewReader(url.Values{"token": {"admin-secret"}, "next": {"/\\evil.example"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e2.http.URL)
	req.Header.Set("X-Forwarded-Proto", "https")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("redirect after login = %q", loc)
	}
	if ck := resp.Header.Get("Set-Cookie"); !strings.Contains(ck, "Secure") {
		t.Errorf("cookie behind HTTPS proxy not Secure: %s", ck)
	}
}
