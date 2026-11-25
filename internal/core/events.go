// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
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
	// Changes of a sync, for "Was ist neu" (V-3, T-9.30).
	Changes *SyncChanges `json:"changes,omitempty"`
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
	// Bounded log (R-026): at maxActivityBytes the file becomes
	// activity.1.jsonl (the one before becomes .2; older ones go).
	if st, err := os.Stat(l.activity.path); err == nil && st.Size() > maxActivityBytes {
		_ = os.Rename(l.activity.older(1), l.activity.older(2))
		if err := os.Rename(l.activity.path, l.activity.older(1)); err != nil {
			l.Log.Warn("activity log rotation", "err", err)
		}
	}
	f, err := os.OpenFile(l.activity.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		l.Log.Warn("activity log", "err", err)
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// maxActivityBytes bounds activity.jsonl before it is rotated.
const maxActivityBytes = 5 << 20

func (a *activityLog) older(i int) string {
	return strings.TrimSuffix(a.path, ".jsonl") + "." + strconv.Itoa(i) + ".jsonl"
}

// RecentActivity returns the newest n entries, optionally for one repo.
// The current file is read first; the previous one only if needed.
func (l *Library) RecentActivity(n int, repoID string) []Activity {
	l.activity.mu.Lock()
	defer l.activity.mu.Unlock()
	var all []Activity
	for _, p := range []string{l.activity.path, l.activity.older(1)} {
		part := readActivity(p, repoID)
		all = append(part, all...)
		if len(all) >= n {
			break
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

func readActivity(p, repoID string) []Activity {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Activity
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var a Activity
		if json.Unmarshal(sc.Bytes(), &a) == nil && (repoID == "" || a.RepoID == repoID) {
			out = append(out, a)
		}
	}
	return out
}
