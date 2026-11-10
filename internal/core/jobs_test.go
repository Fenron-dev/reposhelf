// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

func TestJobProgress(t *testing.T) {
	l, _ := openLib(t, testutil.NewForge(t))
	events, cancel := l.Subscribe()
	defer cancel()

	// Progress of a repository that is not syncing is ignored.
	l.progress("r1", "Code wird geholt", 10)
	if _, ok := l.Job("r1"); ok {
		t.Fatal("idle repository has a job")
	}

	l.jobs.mu.Lock()
	l.jobs.running["r1"] = JobState{RepoID: "r1", Key: "h/o/n", Running: true, Phase: "startet", Percent: -1}
	l.jobs.mu.Unlock()

	l.progress("r1", "Code wird geholt", 10)
	l.progress("r1", "Code wird geholt", 20) // same phase, throttled
	l.progress("r1", "Metadaten", -1)        // new phase, always sent
	js, ok := l.Job("r1")
	if !ok || js.Phase != "Metadaten" || js.Percent != -1 {
		t.Fatalf("job = %+v, %v", js, ok)
	}
	var texts []string
	for len(events) > 0 {
		if e := <-events; e.Kind == "job" {
			texts = append(texts, e.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "Code wird geholt" || texts[1] != "Metadaten" {
		t.Errorf("job events = %v", texts)
	}
}
