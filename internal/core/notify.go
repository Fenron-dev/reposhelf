// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// ---- Notifications (V-2, T-9.22) ----
//
// Important changes are kept as messages (shown in the UI with a counter
// and as a short notice while it is open) and, if set up, sent to a
// webhook (ntfy, Discord or plain JSON). The webhook address can carry a
// secret (Discord's key, an ntfy topic): it lives in the system keychain
// or REPOSHELF_NOTIFY_URL, never in reposhelf.yaml.

// Kinds of notifications; also the names of the settings checkboxes.
const (
	NotifyVersion   = "new_version" // a new tag
	NotifyGone      = "gone"        // original deleted or made private
	NotifyArchived  = "archived"    // original archived
	NotifyRewritten = "rewritten"   // force-push, old state backed up
	NotifyFailing   = "failing"     // sync failing for failingAfter
	NotifyIntegrity = "integrity"   // damage found by the integrity check
	NotifyRevive    = "revive"      // a revive finished or failed
	NotifyStorage   = "storage"     // collection over its limit, free space running out
	NotifyUpdate    = "update"      // a new RepoShelf version
)

// NotifyKinds lists every kind (all are on unless switched off).
var NotifyKinds = []string{NotifyVersion, NotifyGone, NotifyArchived, NotifyRewritten, NotifyFailing, NotifyIntegrity, NotifyRevive, NotifyStorage, NotifyUpdate}

const (
	notifyFile = "notifications.jsonl"
	// maxNotifications kept in the file; older ones are dropped.
	maxNotifications = 500
	// failingAfter: a sync failing that long is reported once.
	failingAfter = 3 * 24 * time.Hour
	// NotifySecret is the keychain account of the webhook address.
	NotifySecret = "reposhelf-notify"
	// EnvNotifyURL sets the webhook address without keychain (Docker).
	EnvNotifyURL = "REPOSHELF_NOTIFY_URL"
	webhookTime  = 10 * time.Second
)

// Notification is one message.
type Notification struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	RepoID string    `json:"repo_id,omitempty"`
	Key    string    `json:"key,omitempty"`
	Title  string    `json:"title"`
	Text   string    `json:"text,omitempty"`
	Read   bool      `json:"read,omitempty"`
}

var notifyMu sync.Mutex

func (l *Library) notifyPath() string { return l.Layout.Path(layout.DirInternal, notifyFile) }

// Notify records a message and sends it to the webhook (in the background)
// unless the kind is switched off.
func (l *Library) Notify(n Notification) {
	cfg := l.Config().Notify
	if slices.Contains(cfg.Off, n.Kind) {
		return
	}
	n.ID = repometa.NewID(l.now())
	if n.Time.IsZero() {
		n.Time = l.now().UTC().Truncate(time.Second)
	}
	notifyMu.Lock()
	all := append(l.readNotifications(), n)
	if len(all) > maxNotifications {
		all = all[len(all)-maxNotifications:]
	}
	err := l.writeNotifications(all)
	notifyMu.Unlock()
	if err != nil {
		l.Log.Warn("notification", "err", err)
	}
	l.events.publish(Event{Kind: "notify", RepoID: n.RepoID, Text: n.Title})
	if u := l.notifyURL(); u != "" {
		go func() {
			if err := l.sendWebhook(context.Background(), u, cfg.WebhookKind, n); err != nil {
				l.Log.Warn("notification webhook", "err", err)
			}
		}()
	}
}

// Notifications returns the messages, newest first.
func (l *Library) Notifications() []Notification {
	notifyMu.Lock()
	all := l.readNotifications()
	notifyMu.Unlock()
	slices.Reverse(all)
	return all
}

// UnreadNotifications counts the unread messages.
func (l *Library) UnreadNotifications() int {
	n := 0
	for _, m := range l.Notifications() {
		if !m.Read {
			n++
		}
	}
	return n
}

// MarkNotificationsRead marks every message as read.
func (l *Library) MarkNotificationsRead() error {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	all := l.readNotifications()
	for i := range all {
		all[i].Read = true
	}
	return l.writeNotifications(all)
}

func (l *Library) readNotifications() []Notification {
	f, err := os.Open(l.notifyPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Notification
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var n Notification
		if json.Unmarshal(sc.Bytes(), &n) == nil {
			out = append(out, n)
		}
	}
	return out
}

func (l *Library) writeNotifications(all []Notification) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, n := range all {
		if err := enc.Encode(n); err != nil {
			return err
		}
	}
	return atomicfile.WriteFile(l.notifyPath(), b.Bytes(), filePerm)
}

// notifyURL is the webhook address: environment first, then keychain.
func (l *Library) notifyURL() string {
	if v, ok := l.lookupEnv(EnvNotifyURL); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	v, _ := l.Tokens.Secret(NotifySecret)
	return v
}

// NotifyWebhook tells whether a webhook is set and where it comes from.
func (l *Library) NotifyWebhook() (set bool, fromEnv bool) {
	if v, ok := l.lookupEnv(EnvNotifyURL); ok && strings.TrimSpace(v) != "" {
		return true, true
	}
	v, _ := l.Tokens.Secret(NotifySecret)
	return v != "", false
}

// SetNotifyWebhook stores (or with "" removes) the webhook address.
func (l *Library) SetNotifyWebhook(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return l.Tokens.RemoveSecret(NotifySecret)
	}
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return fmt.Errorf("%w: Webhook muss mit https:// oder http:// beginnen", ErrInvalid)
	}
	return l.Tokens.SaveSecret(NotifySecret, u)
}

// TestNotify sends a test message.
func (l *Library) TestNotify(ctx context.Context) error {
	u := l.notifyURL()
	if u == "" {
		return fmt.Errorf("%w: kein Webhook eingerichtet", ErrInvalid)
	}
	return l.sendWebhook(ctx, u, l.Config().Notify.WebhookKind, Notification{Kind: "test", Title: "RepoShelf: Test",
		Text: "Benachrichtigungen kommen an.", Time: l.now().UTC()})
}

// sendWebhook delivers one message. The address is set by the user, so
// addresses in the home network (a self-hosted ntfy) are fine here.
func (l *Library) sendWebhook(ctx context.Context, u, kind string, n Notification) error {
	ctx, cancel := context.WithTimeout(ctx, webhookTime)
	defer cancel()
	link := ""
	if base := strings.TrimSuffix(l.Config().Server.PublicURL, "/"); base != "" && n.RepoID != "" {
		link = base + "/repo/" + n.RepoID
	}
	var req *http.Request
	var err error
	switch kind {
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(strings.TrimSpace(n.Text)))
		if err == nil {
			req.Header.Set("Title", n.Title)
			req.Header.Set("Tags", n.Kind)
			if link != "" {
				req.Header.Set("Click", link)
			}
		}
	case "discord":
		msg := "**" + n.Title + "**"
		if n.Text != "" {
			msg += "\n" + n.Text
		}
		if link != "" {
			msg += "\n" + link
		}
		b, _ := json.Marshal(map[string]string{"content": msg})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		b, _ := json.Marshal(map[string]any{"kind": n.Kind, "title": n.Title, "text": n.Text, "repo": n.Key, "url": link, "time": n.Time})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "RepoShelf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return nil
}

// notifySync turns the result of a sync into messages.
func (l *Library) notifySync(r *Repo, first bool, before string, rep SyncReport) {
	if first {
		return // a new repository has "new" everything
	}
	key, id := r.Entry.Key, r.R().ID
	var tags []string
	for _, c := range rep.Mirror.New {
		if t, ok := strings.CutPrefix(c.Ref, "refs/tags/"); ok {
			tags = append(tags, t)
		}
	}
	if len(tags) > 0 {
		l.Notify(Notification{Kind: NotifyVersion, RepoID: id, Key: key,
			Title: fmt.Sprintf("Neue Version von %s: %s", key, strings.Join(tags, ", "))})
	}
	if len(rep.Mirror.Rewritten) > 0 {
		l.Notify(Notification{Kind: NotifyRewritten, RepoID: id, Key: key,
			Title: "Geschichte umgeschrieben: " + key,
			Text:  fmt.Sprintf("%d Branches/Tags wurden beim Original überschrieben; der alte Stand ist gesichert (%s).", len(rep.Mirror.Rewritten), rep.Mirror.Backup)})
	}
	if after := rep.Status; after != before {
		switch after {
		case StatusGone:
			l.Notify(Notification{Kind: NotifyGone, RepoID: id, Key: key, Title: "Original verschwunden: " + key,
				Text: "Gelöscht oder privat geworden. Die Sicherung in RepoShelf bleibt; „Wiederbeleben“ lädt sie auf Wunsch neu hoch."})
		case StatusArchived:
			l.Notify(Notification{Kind: NotifyArchived, RepoID: id, Key: key, Title: "Original archiviert: " + key})
		}
	}
	up := &r.Upstream
	switch {
	case up.FailingSince == nil:
		up.FailingNotified = false
	case !up.FailingNotified && l.now().Sub(*up.FailingSince) >= failingAfter:
		up.FailingNotified = true
		l.Notify(Notification{Kind: NotifyFailing, RepoID: id, Key: key, Title: "Abgleich schlägt seit Tagen fehl: " + key,
			Text: "Seit " + up.FailingSince.Local().Format("02.01.2006") + "."})
	}
}
