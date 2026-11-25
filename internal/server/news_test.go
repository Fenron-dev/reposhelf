// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestNewsBlock: the library shows "Was ist neu" since the last visit and
// "Alles gesehen" moves the start to now (V-3).
func TestNewsBlock(t *testing.T) {
	e := setup(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func() string {
		t.Helper()
		resp, err := c.Get(e.http.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	// First visit: the repositories added during setup are new.
	body := get()
	if !strings.Contains(body, "Was ist neu") || !strings.Contains(body, "neu in der Bibliothek") {
		t.Fatalf("no news block:\n%s", body)
	}
	u, _ := url.Parse(e.http.URL)
	var since string
	for _, ck := range jar.Cookies(u) {
		if ck.Name == "rs_since" {
			since = ck.Value
		}
	}
	if sec, err := strconv.ParseInt(since, 10, 64); err != nil || time.Since(time.Unix(sec, 0)) < 6*24*time.Hour {
		t.Errorf("first visit since %q, want a week back", since)
	}
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+"/news/seen", nil)
	req.Header.Set("Origin", e.http.URL)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("seen: %d", resp.StatusCode)
	}
	if body := get(); strings.Contains(body, "neu in der Bibliothek") || !strings.Contains(body, "Seitdem nichts Neues") {
		t.Errorf("still new after 'Alles gesehen'")
	}
	// A filtered list has no block.
	if body := e.get("/?q=fero", 200); strings.Contains(body, "Was ist neu") {
		t.Error("news block on a search")
	}
}
