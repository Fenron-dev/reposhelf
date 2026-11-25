// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"strings"
	"testing"
)

// TestHistoryPages: commit list, one commit and comparing a version with
// HEAD (V-6); invalid revisions are rejected, unknown ones not found.
func TestHistoryPages(t *testing.T) {
	e := setup(t)
	id := e.repo.R().ID
	list := e.get("/repo/"+id+"/commits", 200)
	for _, want := range []string{">code<", ">docs<", "Seite 1"} {
		if !strings.Contains(list, want) {
			t.Errorf("commit list lacks %q", want)
		}
	}
	if strings.Contains(list, "Ältere →") {
		t.Error("pager offers older commits on a short history")
	}
	c := e.get("/repo/"+id+"/commit/v1.0", 200)
	for _, want := range []string{"main.go", "func main() {}", "class=\"add\"", "+3"} {
		if !strings.Contains(c, want) {
			t.Errorf("commit page lacks %q", want)
		}
	}
	cmp := e.get("/repo/"+id+"/compare?from=v1.0~1&to=v1.0", 200)
	if !strings.Contains(cmp, "1 Commit von") || !strings.Contains(cmp, "main.go") || strings.Contains(cmp, "guide.md</a>") {
		t.Errorf("compare page:\n%s", cmp)
	}
	e.get("/repo/"+id+"/compare", 200) // form only
	e.get("/repo/"+id+"/commit/--output=x", 400)
	e.get("/repo/"+id+"/commit/0123456789abcdef", 404)
	e.get("/repo/"+id+"/compare?from=gibtsnicht", 404)
	if body := e.get("/repo/"+id+"?tab=versions", 200); !strings.Contains(body, "Zwei Stände vergleichen") {
		t.Error("versions tab without compare link")
	}
}
