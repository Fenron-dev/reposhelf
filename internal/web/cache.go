// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// Pages must not start many git processes: on Windows each one costs
// noticeable time (T-9.9). Results that only change with a sync or push
// are cached in memory; the cleanup review is computed in the background.

// reviewTTL is how long a computed cleanup review is shown before it is
// recomputed in the background.
const reviewTTL = 5 * time.Minute

// reviewWait is how long a page waits for a review being computed before
// it shows the previous one.
const reviewWait = 300 * time.Millisecond

type reviewCache struct {
	mu       sync.Mutex
	value    core.StartupReview
	computed time.Time
	running  chan struct{} // closed when the running computation is done
}

// review returns the cleanup review. When it is older than reviewTTL it is
// recomputed in the background; the page waits at most reviewWait for it.
func (s *Server) review() core.StartupReview {
	c := &s.reviews
	c.mu.Lock()
	if c.running == nil && time.Since(c.computed) > reviewTTL {
		done := make(chan struct{})
		c.running = done
		go func() {
			rv, err := s.Lib.Review(context.Background())
			c.mu.Lock()
			if err == nil {
				c.value = rv
			}
			c.computed, c.running = time.Now(), nil
			c.mu.Unlock()
			close(done)
		}()
	}
	running := c.running
	c.mu.Unlock()
	if running != nil {
		select {
		case <-running:
		case <-time.After(reviewWait):
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// freshReview recomputes the review after every change made through the
// interface (marking, settings, deleting, …).
func (s *Server) freshReview(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			s.invalidateReview()
		}
	})
}

// invalidateReview makes the next page recompute the review.
func (s *Server) invalidateReview() {
	s.reviews.mu.Lock()
	s.reviews.computed = time.Time{}
	s.reviews.mu.Unlock()
}

// overview is the cached README and recent commits of a repository.
type overview struct {
	ReadmeName string
	Readme     template.HTML
	Commits    []gitcli.Commit
}

type overviewCache struct {
	mu sync.Mutex
	m  map[string]overview
}

const overviewCacheMax = 200

// overviewFor returns README and commits, read from git only when the
// repository changed since the last view.
func (s *Server) overviewFor(ctx context.Context, repo *core.Repo) overview {
	st := repo.Upstream.Stats
	key := fmt.Sprintf("%s|%v|%v|%d", repo.R().ID, st.LastCommit, repo.R().Sync.LastSuccess, st.SizeBytes)
	c := &s.overviews
	c.mu.Lock()
	if ov, ok := c.m[key]; ok {
		c.mu.Unlock()
		return ov
	}
	c.mu.Unlock()

	var ov overview
	mirror := repo.Paths.Mirror
	if name, b, err := s.Lib.Git.Readme(ctx, mirror, "HEAD", maxView); err == nil && name != "" {
		ov.ReadmeName = name
		if isMarkdown(name) {
			ov.Readme = s.renderMarkdown(b, repo.R().ID, "HEAD", "")
		} else {
			ov.Readme = template.HTML("<pre>" + template.HTMLEscapeString(string(b)) + "</pre>") //nolint:gosec // escaped
		}
	}
	ov.Commits, _ = s.Lib.Git.Log(ctx, mirror, "HEAD", 5)

	c.mu.Lock()
	if c.m == nil || len(c.m) >= overviewCacheMax {
		c.m = map[string]overview{}
	}
	c.m[key] = ov
	c.mu.Unlock()
	return ov
}

// hasContent tells from the stored statistics whether a repository has
// commits, without starting git.
func hasContent(repo *core.Repo) bool {
	st := repo.Upstream.Stats
	return st.LastCommit != nil || st.Branches > 0
}
