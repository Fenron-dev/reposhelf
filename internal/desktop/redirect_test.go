// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFollowRedirects(t *testing.T) {
	h := FollowRedirects(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/save" {
			http.Redirect(w, r, "/repo/01ABC?ok=Gespeichert", http.StatusSeeOther)
			return
		}
		w.Write([]byte("page"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/save", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Location") != "" {
		t.Fatalf("code %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(body, `http-equiv="refresh" content="0;url=/repo/01ABC?ok=Gespeichert"`) || strings.Contains(body, "See Other") {
		t.Errorf("body = %s", body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != 200 || rec.Body.String() != "page" {
		t.Errorf("normal page changed: %d %q", rec.Code, rec.Body.String())
	}
}
