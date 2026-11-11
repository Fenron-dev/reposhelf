// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestIssuesToOriginal: own projects change issues locally; changes wait
// until sent (switch off) or go out at once (switch on) – D-031, T-9.16.
func TestIssuesToOriginal(t *testing.T) {
	ctx := context.Background()
	f := testutil.NewForge(t)
	f.NewRepo("me", "tool")
	f.Issues["me/tool"] = []*testutil.FakeIssue{{Number: 1, Title: "Absturz", Body: "beim Start", State: "open"}}
	l, _ := openLib(t, f)
	inc := repometa.Defaults(repometa.KindOwn).Include
	inc.Issues = true
	r, err := l.Add(ctx, AddRequest{URL: f.URL("me", "tool"), Kind: repometa.KindOwn, Include: &inc, Mode: repometa.SyncManual, Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	id := r.R().ID
	m1 := IssueRef{Number: 1}

	// Switch off: everything stays private.
	if err := l.CommentIssue(ctx, id, m1, "Liegt an der Konfiguration", "mcp:claude"); err != nil {
		t.Fatal(err)
	}
	n, err := l.CreateIssue(ctx, id, "Neue Idee", "Export als PDF", []string{"idee"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetIssueState(ctx, id, IssueRef{Local: true, Number: n}, IssueClosed, "", "web"); err != nil {
		t.Fatal(err)
	}
	if len(f.Issues["me/tool"]) != 1 || len(f.Issues["me/tool"][0].Comments) != 0 {
		t.Fatal("something left RepoShelf with the switch off")
	}
	list, _ := l.Issues(ctx, id)
	pending := 0
	for _, is := range list {
		pending += len(is.Pending)
	}
	if pending != 3 {
		t.Errorf("pending = %d, want 3 (comment, create, close)", pending)
	}

	// Send by hand: in order, the new issue first, then its state.
	sent, failed, err := l.SendIssues(ctx, id, "web")
	if err != nil || sent != 3 || failed != 0 {
		t.Fatalf("send = %d/%d, %v", sent, failed, err)
	}
	gh := f.Issues["me/tool"]
	if len(gh) != 2 || !strings.Contains(gh[0].Comments[0], "über RepoShelf, von mcp:claude") || gh[1].Title != "Neue Idee" || gh[1].State != "closed" {
		t.Errorf("host issues = %+v %+v", gh[0], gh[1])
	}
	if len(l.PendingIssues(r)) != 0 {
		t.Error("outbox not empty")
	}
	// The sent local issue now shows as the mirrored #2, not twice.
	list, _ = l.Issues(ctx, id)
	if len(list) != 2 {
		t.Errorf("issues after send = %+v", list)
	}
	// A new change to L<n> waits under #2 where it is visible (R-005).
	if err := l.CommentIssue(ctx, id, IssueRef{Local: true, Number: n}, "Nachtrag", "web"); err != nil {
		t.Fatal(err)
	}
	list, _ = l.Issues(ctx, id)
	for _, is := range list {
		if is.Number == 2 && !is.Local && len(is.Pending) != 1 {
			t.Errorf("#2 pending = %+v", is.Pending)
		}
	}
	if _, _, err := l.SendIssues(ctx, id, "web"); err != nil || len(f.Issues["me/tool"][1].Comments) != 1 {
		t.Errorf("Nachtrag not sent: %v %+v", err, f.Issues["me/tool"][1])
	}

	// Switch on: changes go out at once.
	s := Settings{Kind: repometa.KindOwn, Mode: repometa.SyncManual, Include: inc, IssuesUpstream: true}
	if _, err := l.UpdateSettings(ctx, id, s, "web"); err != nil {
		t.Fatal(err)
	}
	if err := l.SetIssueState(ctx, id, m1, IssueClosed, "Behoben", "web"); err != nil {
		t.Fatal(err)
	}
	if gh[0].State != "closed" || len(gh[0].Comments) != 2 || len(l.PendingIssues(r)) != 0 {
		t.Errorf("immediate send: %+v, pending %d", gh[0], len(l.PendingIssues(r)))
	}

	// A failing host keeps the change with its error.
	f.Gone["me/tool"] = true
	l.CommentIssue(ctx, id, m1, "noch was", "web")
	if p := l.PendingIssues(r); len(p) != 1 || p[0].Error == "" {
		t.Errorf("failed change = %+v", p)
	}
	delete(f.Gone, "me/tool")

	// Mirrors stay read-only.
	f.NewRepo("other", "lib")
	f.Issues["other/lib"] = []*testutil.FakeIssue{{Number: 1, Title: "x", State: "open"}}
	mr, _ := l.Add(ctx, AddRequest{URL: f.URL("other", "lib"), Include: &inc, Mode: repometa.SyncManual, Wait: true})
	if err := l.CommentIssue(ctx, mr.R().ID, m1, "x", "web"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("comment on mirror = %v", err)
	}
}
