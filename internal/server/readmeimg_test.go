// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"strings"
	"testing"
)

// TestPagesLoadNoForeignImages: the page policy only allows images from
// RepoShelf itself (R-023); unknown README images are not served.
func TestPagesLoadNoForeignImages(t *testing.T) {
	e := setup(t)
	resp, err := http.Get(e.http.URL + "/repo/" + e.repo.R().ID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data:;") || strings.Contains(csp, "https:") {
		t.Errorf("CSP = %s", csp)
	}
	e.get("/repo/"+e.repo.R().ID+"/readme-asset/unbekannt.png", 404)
	e.get("/static/external-image.svg", 200)
}
