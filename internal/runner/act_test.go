// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

func TestUnsafeWorkflow(t *testing.T) {
	for name, tc := range map[string]struct{ wf, want string }{
		"plain":           {"jobs:\n  a:\n    runs-on: ubuntu-latest\n    steps: [{run: echo hi}]\n", ""},
		"image only":      {"jobs:\n  a:\n    container: node:20\n", ""},
		"container image": {"jobs:\n  a:\n    container: {image: node:20, env: {A: b}}\n", ""},
		"options":         {"jobs:\n  a:\n    container: {image: x, options: --privileged}\n", "container.options"},
		"volumes":         {"jobs:\n  a:\n    container: {image: x, volumes: ['/:/host']}\n", "container.volumes"},
		"service volumes": {"jobs:\n  a:\n    services:\n      db: {image: x, volumes: ['/:/host']}\n", "Dienst db"},
		"other job":       {"jobs:\n  a: {runs-on: ubuntu-latest}\n  b:\n    container: {image: x, options: -v /:/h}\n", "Job b"},
		"not a workflow":  {"jobs: [", "nicht lesbar"},
	} {
		if got := unsafeWorkflow([]byte(tc.wf)); (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: unsafeWorkflow = %q, want %q", name, got, tc.want)
		}
	}
}

// fakeAct records its arguments.
const fakeAct = `#!/bin/sh
echo "$@" > "$FAKEBIN/act-args"
echo "fake act ran"
`

// TestActIsContained: act gets no Docker socket for its job containers,
// workflows with own container options are refused, and a mirror's
// workflow always waits for a person (R-014).
func TestActIsContained(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, lib, _ := setup(t)
	bin := os.Getenv("FAKEBIN")
	os.WriteFile(filepath.Join(bin, "act"), []byte(fakeAct), 0o755)
	m.Act = filepath.Join(bin, "act")
	cfg := lib.Config()
	cfg.Runner.Confirm = false
	lib.SaveConfig(cfg)
	m.Start(ctx)

	project := func(name, workflow string) string {
		dir := filepath.Join(t.TempDir(), name)
		os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755)
		os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte(workflow), 0o644)
		r, err := lib.ImportFolder(ctx, core.ImportRequest{Path: dir, Actor: "web", Wait: true})
		if err != nil {
			t.Fatal(err)
		}
		return r.R().ID
	}
	good := project("gut", "on: push\njobs:\n  hello:\n    runs-on: ubuntu-latest\n    steps: [{run: echo hi}]\n")
	b, err := m.StartBuild(ctx, StartRequest{Repo: good, Target: "act:ci/hello", Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	b = wait(t, m, b.ID)
	if log, _ := m.BuildLog(b.ID); b.Status != artifact.StatusSuccess {
		t.Fatalf("act build: %s\n%s", b.Error, log)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "act-args"))
	if !strings.Contains(string(args), "--container-daemon-socket -") {
		t.Errorf("act would mount the Docker socket: %s", args)
	}

	bad := project("boese", "on: push\njobs:\n  hello:\n    runs-on: ubuntu-latest\n    container: {image: alpine, volumes: ['/:/host']}\n    steps: [{run: cat /host/etc/shadow}]\n")
	os.Remove(filepath.Join(bin, "act-args"))
	b, _ = m.StartBuild(ctx, StartRequest{Repo: bad, Target: "act:ci/hello", Actor: "web"})
	b = wait(t, m, b.ID)
	if _, err := os.Stat(filepath.Join(bin, "act-args")); b.Status != artifact.StatusFailed || !strings.Contains(b.Error, "abgelehnt") || err == nil {
		t.Errorf("unsafe workflow: status %s, error %q, act started: %v", b.Status, b.Error, err == nil)
	}

	// A mirror's workflow waits for a person even with confirmation off.
	if needsPerson(artifact.KindAct, repometa.KindMirror) != true || needsPerson(artifact.KindAct, repometa.KindOwn) || needsPerson(artifact.KindDocker, repometa.KindMirror) {
		t.Error("needsPerson")
	}
}
