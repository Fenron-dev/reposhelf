// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// TestNotifications: new versions, force-pushes, a vanished original and a
// long-failing sync become messages; the webhook gets them (V-2).
func TestNotifications(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var got []map[string]string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, map[string]string{"title": r.Header.Get("Title"), "body": string(b)})
		mu.Unlock()
	}))
	defer hook.Close()

	f := testutil.NewForge(t)
	up := f.NewRepo("o", "tool")
	l, clk := openLib(t, f)
	l.lookupEnv = func(k string) (string, bool) {
		if k == EnvNotifyURL {
			return hook.URL, true
		}
		return "", false
	}
	cfg := l.Config()
	cfg.Notify.WebhookKind = "ntfy"
	l.SaveConfig(cfg)
	r, err := l.Add(ctx, AddRequest{URL: f.URL("o", "tool"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Notifications()) != 0 {
		t.Fatal("first sync reported")
	}

	up.Commit("a.txt", "a", "a")
	up.Git("tag", "v2.0")
	up.Push()
	up.Git("push", "--quiet", "origin", "--tags")
	l.Sync(ctx, r.R().ID, "t")
	up.Git("reset", "--hard", "HEAD~1")
	up.Git("push", "--quiet", "--force", "origin", "main")
	l.Sync(ctx, r.R().ID, "t")

	f.SetGone("o/tool", true)
	clk.Add(time.Hour)
	l.Sync(ctx, r.R().ID, "t")
	clk.Add(4 * 24 * time.Hour)
	l.Sync(ctx, r.R().ID, "t")
	l.Sync(ctx, r.R().ID, "t") // reported once only

	kinds := map[string]int{}
	for _, n := range l.Notifications() {
		kinds[n.Kind]++
	}
	if kinds[NotifyVersion] != 1 || kinds[NotifyRewritten] != 1 || kinds[NotifyGone] != 1 || kinds[NotifyFailing] != 1 {
		t.Errorf("kinds = %v", kinds)
	}
	if l.UnreadNotifications() != 4 {
		t.Errorf("unread = %d", l.UnreadNotifications())
	}
	l.MarkNotificationsRead()
	if l.UnreadNotifications() != 0 {
		t.Error("not marked read")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if len(got) < 4 || !strings.Contains(got[0]["title"], "Neue Version") || !strings.Contains(got[0]["title"], "v2.0") {
		t.Errorf("webhook = %+v", got)
	}
	mu.Unlock()

	// Switched-off kinds are not reported.
	cfg = l.Config()
	cfg.Notify.Off = []string{NotifyIntegrity}
	l.SaveConfig(cfg)
	l.Notify(Notification{Kind: NotifyIntegrity, Title: "x"})
	if len(l.Notifications()) != 4 {
		t.Error("switched-off kind reported")
	}
}

func TestWebhookFormats(t *testing.T) {
	var body []byte
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		hdr = r.Header
	}))
	defer srv.Close()
	l, _ := openLib(t, testutil.NewForge(t))
	n := Notification{Kind: NotifyGone, Title: "Original verschwunden: a/b", Text: "weg", Key: "a/b"}
	if err := l.sendWebhook(context.Background(), srv.URL, "discord", n); err != nil {
		t.Fatal(err)
	}
	var d map[string]string
	if json.Unmarshal(body, &d) != nil || !strings.Contains(d["content"], "**Original verschwunden: a/b**") {
		t.Errorf("discord = %s", body)
	}
	l.sendWebhook(context.Background(), srv.URL, "json", n)
	var j map[string]any
	if json.Unmarshal(body, &j) != nil || j["kind"] != NotifyGone || j["repo"] != "a/b" {
		t.Errorf("json = %s", body)
	}
	l.sendWebhook(context.Background(), srv.URL, "ntfy", n)
	if hdr.Get("Title") != n.Title || string(body) != "weg" {
		t.Errorf("ntfy = %v %s", hdr, body)
	}
}
