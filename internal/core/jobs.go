// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// JobState of a queued or running sync.
type JobState struct {
	RepoID  string
	Key     string
	Running bool
	Queued  time.Time
	Started time.Time
	Actor   string
}

// jobs runs syncs in the background with limited concurrency. A repository
// is never queued twice.
type jobs struct {
	l       *Library
	mu      sync.Mutex
	queue   []JobState
	running map[string]JobState
	limit   int
	wake    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

func newJobs(l *Library) *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{l: l, running: map[string]JobState{}, limit: l.Config().Sync.Concurrency,
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
}

func (j *jobs) resize(n int) {
	j.mu.Lock()
	j.limit = max(1, n)
	j.mu.Unlock()
	j.poke()
}

func (j *jobs) poke() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

func (j *jobs) stop() {
	j.cancel()
	j.wg.Wait()
}

// Enqueue schedules a background sync of repoID. It returns false if the
// repository is already queued or running.
func (l *Library) Enqueue(repoID, key, actor string) bool {
	j := l.jobs
	j.mu.Lock()
	if _, ok := j.running[repoID]; ok {
		j.mu.Unlock()
		return false
	}
	for _, q := range j.queue {
		if q.RepoID == repoID {
			j.mu.Unlock()
			return false
		}
	}
	j.queue = append(j.queue, JobState{RepoID: repoID, Key: key, Queued: l.now(), Actor: actor})
	j.mu.Unlock()
	l.events.publish(Event{Kind: "job", RepoID: repoID})
	j.poke()
	return true
}

// Jobs returns running and queued syncs.
func (l *Library) Jobs() []JobState {
	j := l.jobs
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []JobState
	for _, r := range j.running {
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.Before(out[b].Started) })
	return append(out, j.queue...)
}

// IsBusy reports whether repoID is queued or syncing.
func (l *Library) IsBusy(repoID string) bool {
	j := l.jobs
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.running[repoID]; ok {
		return true
	}
	for _, q := range j.queue {
		if q.RepoID == repoID {
			return true
		}
	}
	return false
}

// StartBackground starts the job workers and the scheduler (server mode).
// CLI commands do not call it; they sync in the foreground.
func (l *Library) StartBackground() {
	j := l.jobs
	j.mu.Lock()
	if j.started {
		j.mu.Unlock()
		return
	}
	j.started = true
	j.mu.Unlock()
	j.wg.Add(2)
	go j.dispatch()
	go j.schedule()
}

func (j *jobs) dispatch() {
	defer j.wg.Done()
	for {
		j.mu.Lock()
		for len(j.queue) > 0 && len(j.running) < j.limit {
			next := j.queue[0]
			j.queue = j.queue[1:]
			next.Running, next.Started = true, j.l.now()
			j.running[next.RepoID] = next
			j.wg.Add(1)
			go j.run(next)
		}
		j.mu.Unlock()
		select {
		case <-j.ctx.Done():
			return
		case <-j.wake:
		}
	}
}

func (j *jobs) run(js JobState) {
	defer j.wg.Done()
	_, err := j.l.Sync(j.ctx, js.RepoID, js.Actor)
	if err != nil {
		j.l.Log.Warn("sync failed", "repo", js.Key, "err", err)
	}
	j.mu.Lock()
	delete(j.running, js.RepoID)
	j.mu.Unlock()
	j.l.events.publish(Event{Kind: "job", RepoID: js.RepoID})
	j.poke()
}

// schedulerTick is how often due repositories are looked for.
const schedulerTick = time.Minute

func (j *jobs) schedule() {
	defer j.wg.Done()
	t := time.NewTicker(schedulerTick)
	defer t.Stop()
	j.l.enqueueDue(j.ctx)
	for {
		select {
		case <-j.ctx.Done():
			return
		case <-t.C:
			j.l.enqueueDue(j.ctx)
		}
	}
}

// enqueueDue queues every repository whose next check is due (T-1.4).
func (l *Library) enqueueDue(ctx context.Context) {
	if l.Config().Sync.Paused {
		return
	}
	entries, err := l.Store.List(ctx, store.Query{})
	if err != nil {
		l.Log.Warn("scheduler", "err", err)
		return
	}
	now := l.now()
	for _, e := range entries {
		if next, ok := l.NextCheck(e); ok && !next.After(now) {
			l.Enqueue(e.ID, e.Key, "scheduler")
		}
	}
}

// NextCheck returns when e is due for its next automatic sync; ok is false
// for repositories that are never synced automatically.
//
//   - follow: last check + interval, plus a fixed per-repository offset of up
//     to 10 % of the interval, so not all repositories hit the host at the
//     same minute (KONZEPT 6.2);
//   - archived or gone upstream: at most every archived_interval (30 days,
//     Fero should_check);
//   - once: only until the first success; manual: never;
//   - local repositories have no upstream.
func (l *Library) NextCheck(e store.Entry) (time.Time, bool) {
	if e.Host == repometa.LocalHost || e.Kind == string(repometa.KindFork) && e.URL == "" {
		return time.Time{}, false
	}
	cfg := l.Config().Sync
	switch repometa.SyncMode(e.SyncMode) {
	case repometa.SyncManual:
		return time.Time{}, false
	case repometa.SyncOnce:
		if e.LastSuccess != nil {
			return time.Time{}, false
		}
		if e.LastCheck == nil {
			return time.Time{}, true
		}
		return e.LastCheck.Add(time.Duration(cfg.DefaultInterval)), true
	}
	interval := e.Interval
	if interval <= 0 {
		interval = time.Duration(cfg.DefaultInterval)
	}
	if e.Status == StatusArchived || e.Status == StatusGone || e.UpstreamArchived {
		interval = max(interval, time.Duration(cfg.ArchivedInterval))
	}
	if e.LastCheck == nil {
		return time.Time{}, true
	}
	return e.LastCheck.Add(interval + spread(e.ID, interval)), true
}

func spread(id string, interval time.Duration) time.Duration {
	h := fnv.New32a()
	h.Write([]byte(id))
	window := interval / 10
	if window <= 0 {
		return 0
	}
	return time.Duration(uint64(h.Sum32()) % uint64(window))
}
