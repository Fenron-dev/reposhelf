// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// CodeQuery describes a code search over all repositories (T-9.5, D-021).
type CodeQuery struct {
	Pattern    string
	Regex      bool // extended regular expression instead of literal text
	IgnoreCase bool
	Tag        string // limit to a tag (prefix/* allowed)
	Repo       string // limit to one repository
	Limit      int    // total hits (default 200)
}

// CodeHit is one matching line.
type CodeHit struct {
	RepoID, Key string
	gitcli.GrepHit
}

// CodeResult is the outcome of a code search.
type CodeResult struct {
	Hits      []CodeHit
	Repos     int  // repositories searched
	Truncated bool // limit or time budget reached
}

const (
	codeSearchTimeout = 20 * time.Second
	codeSearchWorkers = 4
	codeMaxPerFile    = 5
	codeDefaultLimit  = 200
)

// SearchCode runs git grep on the default branch of every repository.
func (l *Library) SearchCode(ctx context.Context, q CodeQuery) (CodeResult, error) {
	var res CodeResult
	if q.Limit <= 0 || q.Limit > 2000 {
		q.Limit = codeDefaultLimit
	}
	var entries []store.Entry
	if q.Repo != "" {
		r, err := l.Get(ctx, q.Repo)
		if err != nil {
			return res, err
		}
		entries = []store.Entry{r.Entry}
	} else {
		var err error
		if entries, err = l.Store.List(ctx, store.Query{Tag: q.Tag, Sort: store.SortName}); err != nil {
			return res, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, codeSearchTimeout)
	defer cancel()
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		jobs = make(chan store.Entry)
		bad  error
	)
	for range codeSearchWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				r, err := l.load(ctx, e.MetaPath)
				if err != nil || !l.Git.HasCommits(ctx, r.Paths.Mirror) {
					continue
				}
				hits, err := l.Git.Grep(ctx, r.Paths.Mirror, "HEAD", q.Pattern, !q.Regex, q.IgnoreCase, codeMaxPerFile)
				mu.Lock()
				res.Repos++
				if errors.Is(err, gitcli.ErrBadPattern) {
					bad = err
				}
				for _, h := range hits {
					if len(res.Hits) >= q.Limit {
						res.Truncated = true
						break
					}
					res.Hits = append(res.Hits, CodeHit{RepoID: e.ID, Key: e.Key, GrepHit: h})
				}
				full := len(res.Hits) >= q.Limit
				mu.Unlock()
				if full {
					cancel()
				}
			}
		}()
	}
	for _, e := range entries {
		select {
		case jobs <- e:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if bad != nil {
		return res, errors.Join(ErrInvalid, bad)
	}
	if ctx.Err() != nil {
		res.Truncated = true
	}
	sort.SliceStable(res.Hits, func(i, j int) bool {
		if res.Hits[i].Key != res.Hits[j].Key {
			return res.Hits[i].Key < res.Hits[j].Key
		}
		if res.Hits[i].Path != res.Hits[j].Path {
			return res.Hits[i].Path < res.Hits[j].Path
		}
		return res.Hits[i].Line < res.Hits[j].Line
	})
	return res, nil
}
