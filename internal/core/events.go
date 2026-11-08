// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event tells surfaces that something changed (live updates in the UI).
type Event struct {
	Kind   string `json:"kind"` // repo, job, reindex, cleanup, build
	RepoID string `json:"repo_id,omitempty"`
	Text   string `json:"text,omitempty"`
}

type events struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func newEvents() *events { return &events{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events and a cancel function.
func (l *Library) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	l.events.mu.Lock()
	l.events.subs[ch] = struct{}{}
	l.events.mu.Unlock()
	return ch, func() {
		l.events.mu.Lock()
		delete(l.events.subs, ch)
		l.events.mu.Unlock()
	}
}

func (e *events) publish(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop, the UI reloads anyway
		}
	}
}

// Activity is one line of the action log: who did what, when (KONZEPT 6.13:
// every MCP action is logged; the UI and CLI log their changes too).
type Activity struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"` // "web", "cli", "mcp:<client>", "scheduler", "api:<token>"
	Action string    `json:"action"`
	RepoID string    `json:"repo_id,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

const activityFile = "activity.jsonl"

type activityLog struct {
	mu   sync.Mutex
	path string
}

// Record appends an entry to the action log.
func (l *Library) Record(a Activity) {
	if a.Time.IsZero() {
		a.Time = l.now().UTC()
	}
	b, err := json.Marshal(a)
	if err != nil {
		return
	}
	l.activity.mu.Lock()
	defer l.activity.mu.Unlock()
	f, err := os.OpenFile(l.activity.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		l.Log.Warn("activity log", "err", err)
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// RecentActivity returns the newest n entries, optionally for one repo.
func (l *Library) RecentActivity(n int, repoID string) []Activity {
	l.activity.mu.Lock()
	defer l.activity.mu.Unlock()
	f, err := os.Open(l.activity.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []Activity
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var a Activity
		if json.Unmarshal(sc.Bytes(), &a) == nil && (repoID == "" || a.RepoID == repoID) {
			all = append(all, a)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all
}
