// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/server"
)

func TestBuildPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, lib, repoID := setup(t)
	m.Start(ctx)
	srv, err := server.New(lib, server.Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatal(err)
	}
	srv.Web.Ext.Builds = m
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	form := url.Values{"repo": {repoID}, "target": {"linux"}}
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/builds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", hs.URL)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 303 {
		t.Fatalf("start = %v %v", resp, err)
	}
	loc := resp.Header.Get("Location")
	b := wait(t, m, strings.TrimPrefix(loc, "/builds/"))
	for _, p := range []string{"/builds", "/builds?repo=" + repoID, loc, loc + "/log", "/repo/" + repoID,
		loc + "/artifact?name=dist/app"} {
		resp, err := http.Get(hs.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.Contains(string(body), "Darstellungsfehler") {
			t.Fatalf("GET %s = %d\n%s", p, resp.StatusCode, body)
		}
		if p == "/repo/"+repoID && !strings.Contains(string(body), "Build-Kapsel") {
			t.Error("repo page lacks capsule card")
		}
	}
	if b.Status != "success" {
		t.Errorf("status = %s", b.Status)
	}
}
