// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/logx"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/testutil"
)

// fakeDocker emulates "docker run … -v SRC:/src … IMAGE sh -c SCRIPT" by
// running SCRIPT in SRC with fake toolchains, so tests need no Docker.
const fakeDocker = `#!/bin/sh
case "$1" in
  version) echo 27.0; exit 0 ;;
  pull) exit 0 ;;
  image) for ref; do :; done
    case "$ref" in
      sha256:*) [ -f "$FAKEBIN/loaded" ] && { echo "$ref"; exit 0; }; exit 1 ;;
      *) echo "sha256:$(printf %064d 7)"; exit 0 ;;
    esac ;;
  load) touch "$FAKEBIN/loaded"; echo "Loaded image: reposhelf/x:1"; exit 0 ;;
  save) shift; out=""; while [ $# -gt 0 ]; do if [ "$1" = "-o" ]; then out="$2"; fi; shift; done
    if [ -n "$out" ]; then echo image > "$out"; else printf 'fake-image-tar'; fi; exit 0 ;;
  build) echo "fake build $*"; [ -f Dockerfile ] || exit 7; touch "$FAKEBIN/built"; exit 0 ;;
  rmi) rm -f "$FAKEBIN/built"; exit 0 ;;
esac
src=""; script=""; net=1
while [ $# -gt 0 ]; do
  case "$1" in
    -v) src="${2%%:/src}"; shift ;;
    -e) export "$2"; shift ;;
    --network) [ "$2" = none ] && net=0; shift ;;
    -c) script="$2"; shift ;;
  esac
  shift
done
echo "fake docker: network=$net"
cd "$src" || exit 9
script=$(printf '%s' "$script" | sed "s#/src/#$src/#g")
PATH="$FAKEBIN:$PATH" NETWORK=$net sh -c "$script"
`

const fakeGo = `#!/bin/sh
if [ "$1 $2" = "mod vendor" ]; then mkdir -p vendor/example.com/dep; echo "package dep" > vendor/example.com/dep/dep.go; echo "# example.com/dep v1.0.0" > vendor/modules.txt; exit 0; fi
if [ "$1 $2" = "mod download" ]; then exit 0; fi
if [ "$1" = "build" ]; then [ "$NETWORK" = 0 ] || { echo "network should be off"; exit 3; }; test -f vendor/modules.txt || { echo "vendor missing"; exit 4; }; echo built; exit 0; fi
exit 0
`

func setup(t *testing.T) (*Manager, *core.Library, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	ctx := context.Background()
	f := testutil.NewForge(t)
	up := f.NewRepo("o", "app")
	up.Commit("go.mod", "module example.com/app\n\ngo 1.24\n", "mod")
	up.Commit("reposhelf-build.yaml", `targets:
  - name: linux
    image: golang:1.24
    commands: ["mkdir -p dist", "echo binary > dist/app", "echo secret=$TOKEN > dist/env.txt"]
    artifacts: ["dist/*"]
    network: false
    secrets: [TOKEN]
`, "build def")
	up.Git("tag", "v1.0")
	up.Push()
	lib, err := core.Open(ctx, core.Options{DataDir: t.TempDir(), Git: f.Git, NoKeychain: true,
		LookupEnv:      func(string) (string, bool) { return "", false },
		SourceKinds:    map[string]string{f.Host: sources.KindGitHub},
		SourceBaseURLs: map[string]string{f.Host: f.APIBase()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	r, err := lib.Add(ctx, core.AddRequest{URL: f.URL("o", "app"), Wait: true})
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDocker), 0o755)
	os.WriteFile(filepath.Join(bin, "go"), []byte(fakeGo), 0o755)
	t.Setenv("FAKEBIN", bin)
	cfg := lib.Config()
	cfg.Runner.Enabled = true
	cfg.Runner.Confirm = true
	cfg.Runner.MinFreeGB = 0
	if err := lib.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := New(lib, logx.Discard())
	m.Docker = filepath.Join(bin, "docker")
	m.LookupEnv = func(k string) (string, bool) {
		if k == "REPOSHELF_SECRET_TOKEN" {
			return "s3cret", true
		}
		return "", false
	}
	return m, lib, r.R().ID
}

func wait(t *testing.T, m *Manager, id string) artifact.Build {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		b, err := m.Get(id)
		if err == nil && b.Done() {
			return b
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("build %s did not finish", id)
	return artifact.Build{}
}

func TestDockerBuildWithConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, _, repoID := setup(t)
	m.Start(ctx)

	targets := m.TargetNames(ctx, repoID)
	if len(targets) != 1 || targets[0] != "linux" {
		t.Fatalf("targets = %v", targets)
	}
	// Agent/MCP builds wait for the human.
	b, err := m.StartBuild(ctx, StartRequest{Repo: repoID, Target: "linux", Actor: "mcp:test"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != artifact.StatusWaiting {
		t.Fatalf("status = %s, want waiting", b.Status)
	}
	if err := m.Confirm(b.ID, "web"); err != nil {
		t.Fatal(err)
	}
	b = wait(t, m, b.ID)
	log, _ := m.BuildLog(b.ID)
	if b.Status != artifact.StatusSuccess {
		t.Fatalf("build failed: %s\n%s", b.Error, log)
	}
	if !strings.Contains(log, "network=0") {
		t.Errorf("offline target ran with network:\n%s", log)
	}
	if len(b.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v", b.Artifacts)
	}
	p, err := m.ArtifactPath(b.ID, "dist/env.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); strings.TrimSpace(string(got)) != "secret=s3cret" {
		t.Errorf("named secret not passed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "work")); !os.IsNotExist(err) {
		t.Error("work folder not cleaned up")
	}
	if _, err := m.StartBuild(ctx, StartRequest{Repo: repoID, Target: "nope", Actor: "t", Confirm: true}); err == nil {
		t.Error("unknown target accepted")
	}
	// MCP adapter.
	bi, err := MCP{M: m}.Get(ctx, b.ID)
	if err != nil || bi.Status != artifact.StatusSuccess || len(bi.Artifacts) != 2 {
		t.Errorf("mcp info = %+v %v", bi, err)
	}
}

func TestCapsuleAndOfflineTest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, lib, repoID := setup(t)
	m.Start(ctx)
	b, err := m.StartBuild(ctx, StartRequest{Repo: repoID, Ref: "v1.0", Target: "capsule", Version: "v1.0", Actor: "web", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	b = wait(t, m, b.ID)
	log, _ := m.BuildLog(b.ID)
	if b.Status != artifact.StatusSuccess {
		t.Fatalf("capsule failed: %s\n%s", b.Error, log)
	}
	caps, _ := m.Capsules(ctx, repoID)
	if len(caps) != 1 || caps[0].Version != "v1.0" || caps[0].FileCount != 2 || caps[0].Ecosystems[0].Name != "go" {
		t.Fatalf("capsules = %+v", caps)
	}
	// Deduplicated store: the same files again add nothing.
	b2, _ := m.StartBuild(ctx, StartRequest{Repo: repoID, Ref: "v1.0", Target: "capsule", Version: "v1.0-b", Actor: "web", Confirm: true})
	wait(t, m, b2.ID)
	used := artifact.Referenced(lib.Layout.Path("capsules"))
	if len(used) != 3 {
		t.Errorf("blobs = %d, want 3: two files and the toolchain image (dedup)", len(used))
	}
	if l2, _ := m.BuildLog(b2.ID); !strings.Contains(l2, "schon gesichert") {
		t.Errorf("toolchain image saved twice:\n%s", l2)
	}
	if caps, _ := m.Capsules(ctx, repoID); caps[0].Ecosystems[0].ImageID == "" {
		t.Error("image id not recorded")
	}

	tb, err := m.StartBuild(ctx, StartRequest{Repo: repoID, Ref: "v1.0", Target: "capsule-test", Version: "v1.0", Actor: "web", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	tb = wait(t, m, tb.ID)
	tlog, _ := m.BuildLog(tb.ID)
	if tb.Status != artifact.StatusSuccess || !strings.Contains(tlog, "built") || !strings.Contains(tlog, "Lade Toolchain-Image") {
		t.Fatalf("capsule test: %s\n%s", tb.Error, tlog)
	}
	caps, _ = m.Capsules(ctx, repoID)
	for _, c := range caps {
		if c.Version == "v1.0" && (c.Test == nil || c.Test.Status != artifact.StatusSuccess) {
			t.Errorf("test result not recorded: %+v", c.Test)
		}
	}

	// Retention: keep 1 capsule → the older one is proposed; blobs in use stay.
	cfg := lib.Config()
	cfg.Retention.CapsulesKeep = 1
	cfg.Retention.BuildsKeep = 1
	lib.SaveConfig(cfg)
	plan, _ := lib.CleanupPlan(ctx)
	kinds := map[string]int{}
	for _, it := range plan {
		kinds[it.Kind]++
	}
	if kinds[core.ItemCapsule] != 1 || kinds[core.ItemBuild] != 2 {
		t.Errorf("plan kinds = %v", kinds)
	}
	var ids []string
	for _, it := range plan {
		ids = append(ids, it.ID)
	}
	if _, err := lib.MoveToTrash(ctx, ids, "t"); err != nil {
		t.Fatal(err)
	}
	if caps, _ := m.Capsules(ctx, repoID); len(caps) != 1 {
		t.Errorf("capsules after trash = %d", len(caps))
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	w := func(p, c string) { os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644) }
	w("go.mod", "module x\n\ngo 1.23\ntoolchain go1.24.2\n")
	w("Cargo.toml", "[package]\n")
	w("rust-toolchain.toml", "[toolchain]\nchannel = \"1.85.0\"\n")
	w("pubspec.yaml", "dependencies:\n  flutter:\n    sdk: flutter\n")
	w("package.json", `{"engines":{"node":">=20"}}`)
	w("package-lock.json", "{}")
	w("requirements.txt", "requests\n")
	w("Dockerfile", "FROM golang:1.24 AS b\nFROM ${BASE}\nFROM scratch\nFROM alpine:3.22\n")
	got := map[string]string{}
	for _, e := range detect(dir) {
		got[e.Name] = e.Image + "|" + e.Toolchain
	}
	want := map[string]string{
		"go": "golang:1.24.2|go 1.24.2", "rust": "rust:1.85.0|rust 1.85.0", "flutter": "ghcr.io/cirruslabs/flutter:stable|flutter stable",
		"node": "node:20|node 20", "python": "python:3|python 3", "docker": "|Basis-Images: golang:1.24, alpine:3.22",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
