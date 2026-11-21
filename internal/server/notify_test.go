// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// TestNotificationPages: messages are listed with a counter; the settings
// switch kinds off (V-2).
func TestNotificationPages(t *testing.T) {
	e := setup(t)
	e.lib.Notify(core.Notification{Kind: core.NotifyVersion, RepoID: e.repo.R().ID, Key: e.repo.Entry.Key, Title: "Neue Version von fero: v2.0"})
	if body := e.get("/", 200); !strings.Contains(body, `badge-new">1<`) {
		t.Error("unread counter missing")
	}
	if body := e.get("/notifications", 200); !strings.Contains(body, "Neue Version von fero: v2.0") {
		t.Error("message not listed")
	}
	e.post("/notifications/read", nil, 303)
	if e.lib.UnreadNotifications() != 0 {
		t.Error("not marked read")
	}
	if body := e.get("/settings", 200); !strings.Contains(body, "Benachrichtigungen") || !strings.Contains(body, "on_new_version") {
		t.Error("settings section missing")
	}
	form := url.Values{"webhook_kind": {"discord"}}
	for _, k := range core.NotifyKinds {
		if k != core.NotifyRewritten {
			form.Set("on_"+k, "on")
		}
	}
	e.post("/settings/notify", form, 303)
	cfg := e.lib.Config()
	if cfg.Notify.WebhookKind != "discord" || !slices.Equal(cfg.Notify.Off, []string{core.NotifyRewritten}) {
		t.Errorf("notify config = %+v", cfg.Notify)
	}
	// No keychain in tests: a webhook cannot be stored, and says so.
	resp := e.post("/settings/notify", url.Values{"webhook": {"https://ntfy.sh/x"}, "webhook_kind": {"ntfy"}}, 303)
	if loc, _ := url.QueryUnescape(resp.Header.Get("Location")); !strings.Contains(loc, "Webhook nicht gespeichert") {
		t.Errorf("redirect = %s", loc)
	}
}
