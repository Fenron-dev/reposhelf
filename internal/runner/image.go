// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// ---- Runtime image (T-9.19) ----
//
// For a project with a Dockerfile the runner builds the finished image and
// keeps it as a build artifact (docker save, gzip): a complete, movable
// environment – e.g. an AI tool with web UI to run on Unraid even when its
// registry or dependencies are gone. The build steps of the Dockerfile run
// in Docker's build containers (D-008).

// TargetImage is the build target name of the runtime image.
const TargetImage = "image"

// dockerfile is where the image definition must be.
const dockerfile = "Dockerfile"

var tagChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// imageTag names the image: reposhelf/<owner>-<name>:<version>.
func imageTag(key, version string) string {
	parts := strings.Split(strings.ToLower(key), "/")
	name := tagChars.ReplaceAllString(strings.Join(parts[1:], "-"), "-")
	ver := tagChars.ReplaceAllString(strings.ToLower(version), "-")
	if ver == "" || strings.Trim(ver, ".-") == "" {
		ver = "latest"
	}
	if len(ver) > 128 {
		ver = ver[:128]
	}
	return "reposhelf/" + strings.Trim(name, "-.") + ":" + strings.TrimLeft(ver, ".-")
}

// hasDockerfile reports whether rev of the mirror has a Dockerfile.
func (m *Manager) hasDockerfile(ctx context.Context, mirror, rev string) bool {
	_, err := m.Lib.Git.ReadFile(ctx, mirror, rev, dockerfile, 1<<20)
	return err == nil
}

// runImage builds the image and stores it as <name>-<version>.tar.gz.
func (m *Manager) runImage(ctx context.Context, b *artifact.Build, log io.Writer) error {
	work := filepath.Join(b.Dir, "work")
	fmt.Fprintf(log, "Quelle auspacken (%s) …\n", short(b.Commit))
	if err := m.checkout(ctx, b, work); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(work, dockerfile)); err != nil {
		return fmt.Errorf("%w: kein %s im Projekt", ErrTarget, dockerfile)
	}
	version := b.Version
	if version == "" {
		version = short(b.Commit)
	}
	tag := imageTag(b.Key, version)
	fmt.Fprintf(log, "docker build -t %s\n", tag)
	if err := m.command(ctx, log, work, m.Docker, "build", "--label", "org.reposhelf.repo="+b.Key,
		"--label", "org.reposhelf.commit="+b.Commit, "-t", tag, "-f", dockerfile, "."); err != nil {
		return err
	}
	// The image only lives in the archive: the runner's Docker gets its
	// space back (it can be loaded again with one click).
	defer func() {
		if err := m.command(context.WithoutCancel(ctx), io.Discard, "", m.Docker, "rmi", tag); err != nil {
			fmt.Fprintf(log, "Hinweis: Image %s bleibt im Docker des Runners: %v\n", tag, err)
		}
	}()
	name := safeName(strings.NewReplacer("reposhelf/", "", ":", "-").Replace(tag)) + ".tar.gz"
	dst := filepath.Join(b.Dir, artifact.ArtifactsDir, name)
	f, err := m.saveImage(ctx, tag, dst)
	if err != nil {
		return err
	}
	f.Name = name
	b.Artifacts = append(b.Artifacts, f)
	fmt.Fprintf(log, "Image gesichert: %s (%d MB). Laden: docker load -i %s\n", name, f.Size>>20, name)
	return nil
}

// saveImage streams docker save through gzip into dst (no tar on disk).
func (m *Manager) saveImage(ctx context.Context, tag, dst string) (artifact.File, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return artifact.File{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".image-*")
	if err != nil {
		return artifact.File{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	count := &countWriter{}
	gz := gzip.NewWriter(io.MultiWriter(tmp, h, count))
	cmd := exec.CommandContext(ctx, m.Docker, "save", tag)
	gitcli.HideWindow(cmd)
	cmd.Stdout = gz
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tmp.Close()
		return artifact.File{}, fmt.Errorf("docker save: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := gz.Close(); err != nil {
		tmp.Close()
		return artifact.File{}, err
	}
	if err := tmp.Close(); err != nil {
		return artifact.File{}, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return artifact.File{}, err
	}
	return artifact.File{Size: count.n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// LoadImage loads an image artifact into the Docker of this instance
// (e.g. Unraid): it then shows up under Docker → Images.
func (m *Manager) LoadImage(ctx context.Context, id, name string) (string, error) {
	p, err := m.ArtifactPath(id, name)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".tar") {
		return "", fmt.Errorf("%w: %s ist kein Image", ErrTarget, name)
	}
	if err := m.dockerOK(ctx); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, m.Docker, "load", "-i", p)
	gitcli.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker load: %v %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
