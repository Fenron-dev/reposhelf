// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/core"
)

// "Was ist neu" on the library (V-3, T-9.30, D-036). The browser keeps the
// time of the last visit in two cookies: rs_last is the last page view,
// rs_since the start of what is shown as new. A page view after a break of
// visitGap moves rs_since to the previous rs_last, so "new" means "since
// you were last here", not "since the last click". Times are in
// milliseconds, so "Alles gesehen" also hides what happened this second.
const (
	cookieSince = "rs_since"
	cookieLast  = "rs_last"
	visitGap    = time.Hour
	// firstVisit: without cookies the block shows the last week.
	firstVisit   = 7 * 24 * time.Hour
	visitMaxAge  = 400 * 24 * 60 * 60 // seconds; browsers cap cookies near this
	newsMaxItems = 8
)

type newsData struct {
	core.News
	More    int // items beyond newsMaxItems
	Storage core.StorageReport
	Over    []core.CollectionUsage
	Open    bool
}

// visitSince returns the start of "new" for this browser and moves the
// visit cookies on.
func visitSince(w http.ResponseWriter, r *http.Request, now time.Time) time.Time {
	read := func(name string) (time.Time, bool) {
		c, err := r.Cookie(name)
		if err != nil {
			return time.Time{}, false
		}
		ms, err := strconv.ParseInt(c.Value, 10, 64)
		if err != nil || ms <= 0 {
			return time.Time{}, false
		}
		return time.UnixMilli(ms), true
	}
	since, okSince := read(cookieSince)
	last, okLast := read(cookieLast)
	switch {
	case !okLast:
		since = now.Add(-firstVisit)
	case now.Sub(last) > visitGap:
		since = last
	case !okSince:
		since = last
	}
	setVisitCookie(w, r, cookieSince, since)
	setVisitCookie(w, r, cookieLast, now)
	return since
}

func setVisitCookie(w http.ResponseWriter, r *http.Request, name string, t time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: strconv.FormatInt(t.UnixMilli(), 10), Path: "/",
		MaxAge: visitMaxAge, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
}

// newsSeen marks everything as seen ("Alles gesehen").
func (s *Server) newsSeen(w http.ResponseWriter, r *http.Request) {
	now := s.Lib.Now()
	setVisitCookie(w, r, cookieSince, now)
	setVisitCookie(w, r, cookieLast, now)
	redirect(w, r, "/")
}

func (s *Server) news(w http.ResponseWriter, r *http.Request) *newsData {
	ctx := r.Context()
	n, err := s.Lib.News(ctx, visitSince(w, r, s.Lib.Now()))
	if err != nil {
		s.Lib.Log.Warn("news", "err", err)
		return nil
	}
	d := &newsData{News: n}
	if rep, err := s.Lib.StorageReport(ctx); err == nil {
		d.Storage = rep
		for _, c := range rep.Collections {
			if c.Over() {
				d.Over = append(d.Over, c)
			}
		}
	}
	if len(d.Items) > newsMaxItems {
		d.More = len(d.Items) - newsMaxItems
		d.Items = d.Items[:newsMaxItems]
	}
	d.Open = !n.Empty() || d.Storage.Short() || len(d.Over) > 0
	return d
}
