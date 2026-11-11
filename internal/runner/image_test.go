// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
)

func TestImageTag(t *testing.T) {
	for key, want := range map[string]string{
		"github.com/Owner/My_App":   "reposhelf/owner-my_app:v1.0",
		"gitlab.com/group/sub/Tool": "reposhelf/group-sub-tool:v1.0",
		"local/modelle/Ein Modell":  "reposhelf/modelle-ein-modell:v1.0",
	} {
		if got := imageTag(key, "v1.0"); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", key, got, want)
		}
	}
	if got := imageTag("a/b/c", "..//"); !strings.HasSuffix(got, ":latest") {
		t.Errorf("odd version = %q", got)
	}
}

// TestRuntimeImage: a project with a Dockerfile gets the target "image";
// the image ends up gzipped in the archive and can be loaded (T-9.19).
func TestRuntimeImage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, lib, appID := setup(t)
	if names := m.TargetNames(ctx, appID); strings.Contains(strings.Join(names, ","), TargetImage) {
		t.Errorf("image offered without Dockerfile: %v", names)
	}
	dir := filepath.Join(t.TempDir(), "webtool")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:3.22\nCMD [\"echo\", \"hi\"]\n"), 0o644)
	repo, err := lib.ImportFolder(ctx, core.ImportRequest{Path: dir, Actor: "web"})
	if err != nil {
		t.Fatal(err)
	}
	id := repo.R().ID
	if names := m.TargetNames(ctx, id); !strings.Contains(strings.Join(names, ","), TargetImage) {
		t.Fatalf("targets = %v", names)
	}
	m.Start(ctx)
	b, err := m.StartBuild(ctx, StartRequest{Repo: id, Ref: "main", Target: TargetImage, Version: "1.0", Actor: "web", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	b = wait(t, m, b.ID)
	log, _ := m.BuildLog(b.ID)
	if b.Status != artifact.StatusSuccess || len(b.Artifacts) != 1 {
		t.Fatalf("image build: %s %+v\n%s", b.Error, b.Artifacts, log)
	}
	name := b.Artifacts[0].Name
	if name != "modelle-webtool-1.0.tar.gz" {
		t.Errorf("artifact = %s", name)
	}
	p, _ := m.ArtifactPath(b.ID, name)
	raw, _ := os.ReadFile(p)
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if content, _ := io.ReadAll(gz); string(content) != "fake-image-tar" {
		t.Errorf("image content = %q", content)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("FAKEBIN"), "built")); err == nil {
		t.Error("image left in the runner's Docker")
	}
	out, err := m.LoadImage(ctx, b.ID, name)
	if err != nil || !strings.Contains(out, "Loaded image") {
		t.Errorf("load = %q, %v", out, err)
	}
	if _, err := m.LoadImage(ctx, b.ID, "../../etc/passwd"); err == nil {
		t.Error("loading an arbitrary path accepted")
	}
}
