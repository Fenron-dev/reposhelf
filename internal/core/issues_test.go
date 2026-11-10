// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func TestLocalIssues(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	l, _ := openLib(t, f)
	r, err := l.CreateLocal(ctx, "", "werkzeug", "", nil, "web")
	if err != nil {
		t.Fatal(err)
	}
	id := r.R().ID

	n1, err := l.CreateIssue(ctx, id, "Export kaputt", "Beim Export fehlt die Datei.", []string{"bug", " bug ", ""}, "web")
	if err != nil || n1 != 1 {
		t.Fatalf("create = %d, %v", n1, err)
	}
	n2, _ := l.CreateIssue(ctx, id, "Neue Funktion\nmit Umbruch", "", nil, "mcp:claude")
	if n2 != 2 {
		t.Errorf("second number = %d", n2)
	}
	if _, err := l.CreateIssue(ctx, id, "  ", "x", nil, "web"); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty title = %v", err)
	}
	if err := l.CommentIssue(ctx, id, 1, "Liegt am Pfad.", "mcp:claude"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetIssueState(ctx, id, 1, IssueClosed, "Behoben in abc123.", "web"); err != nil {
		t.Fatal(err)
	}
	if err := l.CommentIssue(ctx, id, 9, "x", "web"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing issue = %v", err)
	}

	list, err := l.Issues(ctx, id)
	if err != nil || len(list) != 2 {
		t.Fatalf("issues = %+v, %v", list, err)
	}
	open, closed := list[0], list[1]
	if open.Ref() != "L2" || open.Title != "Neue Funktion mit Umbruch" || open.Author != "mcp:claude" || !open.Local {
		t.Errorf("open = %+v", open)
	}
	if closed.State != IssueClosed || closed.Comments != 2 || len(closed.Labels) != 1 ||
		!strings.Contains(closed.Body, "Liegt am Pfad.") || !strings.Contains(closed.Body, "Behoben in abc123.") {
		t.Errorf("closed = %+v", closed)
	}
	if err := l.SetIssueState(ctx, id, 1, IssueOpen, "", "web"); err != nil {
		t.Fatal(err)
	}
	// Files are plain Markdown next to repo.yaml (YAML is the truth).
	b, err := os.ReadFile(filepath.Join(r.Paths.Meta, IssuesDir, LocalIssuesDir, "1.md"))
	if err != nil || !strings.Contains(string(b), "source: local") || !strings.Contains(string(b), "state: open") {
		t.Errorf("file = %s, %v", b, err)
	}
	for _, ref := range []string{"L3", "l3", "3"} {
		if n, err := ParseIssueRef(ref); err != nil || n != 3 {
			t.Errorf("ParseIssueRef(%q) = %d, %v", ref, n, err)
		}
	}
	if _, err := ParseIssueRef("#x"); err == nil {
		t.Error("bad ref accepted")
	}
}
