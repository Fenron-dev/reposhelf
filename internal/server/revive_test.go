// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"strings"
	"testing"
)

// TestRevivePage: the form is offered to administrators; without a token
// for a host it says where to add one (V-5).
func TestRevivePage(t *testing.T) {
	e := setup(t)
	id := e.repo.R().ID
	if body := e.get("/repo/"+id+"?tab=settings", 200); !strings.Contains(body, "Wiederbeleben") || !strings.Contains(body, "Dafür fehlt ein Token") {
		t.Error("revive section missing or wrong")
	}
	e.post("/repo/"+id+"/revive", url.Values{"host": {"github.com"}, "name": {"neu"}}, 400)
}
