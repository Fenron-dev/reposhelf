// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// capsuleDir inside the checkout collects everything a capsule keeps
// besides vendor folders.
const capsuleDir = ".reposhelf-capsule"

// eco describes how one ecosystem fetches its dependencies (online) and
// builds from them (offline, capsule test). KONZEPT 6.11 table.
type eco struct {
	Name, Image, Toolchain string
	Fetch, Build           string
	// Keep lists paths (relative to the checkout) that make up the capsule.
	Keep []string
}

var (
	goVersion   = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)`)
	goToolchain = regexp.MustCompile(`(?m)^toolchain\s+go(\d+\.\d+(?:\.\d+)?)`)
	rustChannel = regexp.MustCompile(`(?m)channel\s*=\s*"([^"]+)"`)
	nodeEngine  = regexp.MustCompile(`"node"\s*:\s*"[^\d]*(\d+)`)
	fromLine    = regexp.MustCompile(`(?mi)^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)`)
)

// detect finds the ecosystems at the root of the checkout.
func detect(work string) []eco {
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(work, p))
		return string(b)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(work, p)); return err == nil }
	var out []eco
	if exists("go.mod") {
		mod := read("go.mod")
		v := "1"
		if m := goToolchain.FindStringSubmatch(mod); m != nil {
			v = m[1]
		} else if m := goVersion.FindStringSubmatch(mod); m != nil {
			v = m[1]
		}
		out = append(out, eco{Name: "go", Image: "golang:" + v, Toolchain: "go " + v,
			Fetch: "go mod download && go mod vendor", Build: "go build -mod=vendor ./...", Keep: []string{"vendor"}})
	}
	if exists("Cargo.toml") {
		ch := "stable"
		for _, f := range []string{"rust-toolchain.toml", "rust-toolchain"} {
			if t := read(f); t != "" {
				if m := rustChannel.FindStringSubmatch(t); m != nil {
					ch = m[1]
				} else if s := strings.TrimSpace(t); !strings.Contains(s, "\n") {
					ch = s
				}
			}
		}
		img := "rust:latest"
		if ch != "stable" && regexp.MustCompile(`^\d+\.\d+`).MatchString(ch) {
			img = "rust:" + ch
		}
		out = append(out, eco{Name: "rust", Image: img, Toolchain: "rust " + ch,
			Fetch: "mkdir -p " + capsuleDir + " && cargo vendor --locked vendor > " + capsuleDir + "/cargo-config.toml",
			Build: "mkdir -p .cargo && cp " + capsuleDir + "/cargo-config.toml .cargo/config.toml && cargo build --offline --frozen",
			Keep:  []string{"vendor", capsuleDir + "/cargo-config.toml"}})
	}
	if exists("pubspec.yaml") {
		spec := read("pubspec.yaml")
		e := eco{Name: "dart", Image: "dart:stable", Toolchain: "dart stable",
			Fetch: "PUB_CACHE=/src/" + capsuleDir + "/pub-cache dart pub get",
			Build: "PUB_CACHE=/src/" + capsuleDir + "/pub-cache dart pub get --offline", Keep: []string{capsuleDir + "/pub-cache"}}
		if strings.Contains(spec, "sdk: flutter") {
			e.Name, e.Image, e.Toolchain = "flutter", "ghcr.io/cirruslabs/flutter:stable", "flutter stable"
			e.Fetch = "PUB_CACHE=/src/" + capsuleDir + "/pub-cache flutter pub get"
			e.Build = "PUB_CACHE=/src/" + capsuleDir + "/pub-cache flutter pub get --offline"
		}
		out = append(out, e)
	}
	if exists("package.json") && exists("package-lock.json") {
		v := "lts"
		if n := strings.TrimSpace(read(".nvmrc")); n != "" {
			v = strings.TrimPrefix(n, "v")
		} else if m := nodeEngine.FindStringSubmatch(read("package.json")); m != nil {
			v = m[1]
		}
		out = append(out, eco{Name: "node", Image: "node:" + v, Toolchain: "node " + v,
			Fetch: "npm ci --ignore-scripts --cache /src/" + capsuleDir + "/npm-cache",
			Build: "npm ci --offline --ignore-scripts --cache /src/" + capsuleDir + "/npm-cache", Keep: []string{capsuleDir + "/npm-cache"}})
	}
	reqs := ""
	for _, f := range []string{"requirements.txt", "requirements-dev.txt"} {
		if exists(f) {
			reqs += " -r " + f
		}
	}
	if reqs != "" || exists("pyproject.toml") {
		v := strings.TrimSpace(read(".python-version"))
		if v == "" {
			v = "3"
		}
		src := reqs
		if src == "" {
			src = " ."
		}
		out = append(out, eco{Name: "python", Image: "python:" + v, Toolchain: "python " + v,
			Fetch: "pip download -d /src/" + capsuleDir + "/wheels" + src,
			Build: "python -m venv /tmp/v && /tmp/v/bin/pip install --no-index --find-links /src/" + capsuleDir + "/wheels" + src,
			Keep:  []string{capsuleDir + "/wheels"}})
	}
	if exists("Dockerfile") {
		var images []string
		for _, m := range fromLine.FindAllStringSubmatch(read("Dockerfile"), -1) {
			if !strings.Contains(m[1], "$") && !strings.EqualFold(m[1], "scratch") {
				images = append(images, m[1])
			}
		}
		if len(images) > 0 {
			out = append(out, eco{Name: "docker", Toolchain: "Basis-Images: " + strings.Join(images, ", "), Fetch: strings.Join(images, " "),
				Keep: []string{capsuleDir + "/images"}})
		}
	}
	return out
}

// runCapsule creates a build capsule for b.Commit (T-6.1–T-6.5): fetch
// dependencies per ecosystem in a container (with network), then store the
// results in the shared blob store and write the manifest.
func (m *Manager) runCapsule(ctx context.Context, b *artifact.Build, log io.Writer) error {
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	work := filepath.Join(b.Dir, "work")
	if err := m.checkout(ctx, b, work); err != nil {
		return err
	}
	ecos := detect(work)
	if len(ecos) == 0 {
		return errors.New("kein unterstütztes Ökosystem gefunden (Go, Rust, Dart/Flutter, Node mit package-lock.json, Python, Dockerfile)")
	}
	version := b.Version
	if version == "" {
		version = b.Ref
	}
	c := artifact.Capsule{RepoID: b.RepoID, Key: b.Key, Version: version, Commit: b.Commit, Created: time.Now().UTC().Truncate(time.Second), BuildID: b.ID}
	blobs := artifact.BlobsOf(m.Lib.Layout.Root)
	var files []artifact.File
	for _, e := range ecos {
		fmt.Fprintf(log, "== %s (%s) ==\n", e.Name, e.Toolchain)
		if e.Name == "docker" {
			if err := m.saveImages(ctx, work, e.Fetch, log); err != nil {
				return err
			}
		} else {
			args := append(m.containerArgs("reposhelf-capsule-"+strings.ToLower(b.ID)+"-"+e.Name, work, true), e.Image, "sh", "-c", "set -e\n"+e.Fetch)
			if err := m.command(ctx, log, "", m.Docker, args...); err != nil {
				return fmt.Errorf("%s: %w", e.Name, err)
			}
		}
		count, size := 0, int64(0)
		for _, keep := range e.Keep {
			root := filepath.Join(work, filepath.FromSlash(keep))
			err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				rel, _ := filepath.Rel(work, p)
				f, _, err := blobs.Put(p, filepath.ToSlash(rel))
				if err != nil {
					return err
				}
				files = append(files, f)
				count++
				size += f.Size
				return nil
			})
			if err != nil {
				return err
			}
		}
		e.Fetch = strings.ReplaceAll(e.Fetch, "/src/", "")
		c.Ecosystems = append(c.Ecosystems, artifact.Ecosystem{Name: e.Name, Toolchain: e.Toolchain, Image: e.Image, Fetch: e.Fetch, Build: e.Build, Files: count, Size: size})
		c.Size += size
		c.FileCount += count
		fmt.Fprintf(log, "%d Dateien, %d Bytes gesichert\n", count, size)
	}
	dir, err := artifact.CapsuleDir(m.Lib.Layout.Root, repo.R().Source)
	if err != nil {
		return err
	}
	if err := artifact.WriteCapsule(dir, c, files); err != nil {
		return err
	}
	fmt.Fprintf(log, "Kapsel %s gespeichert (%d Dateien, gemeinsamer Cache dedupliziert).\n", version, c.FileCount)
	return nil
}

// saveImages pulls the base images of a Dockerfile and saves them as tar
// files into the capsule (docker save).
func (m *Manager) saveImages(ctx context.Context, work, images string, log io.Writer) error {
	out := filepath.Join(work, capsuleDir, "images")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, img := range strings.Fields(images) {
		if err := m.command(ctx, log, "", m.Docker, "pull", img); err != nil {
			return err
		}
		if err := m.command(ctx, log, "", m.Docker, "save", "-o", filepath.Join(out, safeName(img)+".tar"), img); err != nil {
			return err
		}
	}
	return nil
}

// runCapsuleTest proves a capsule is complete: build offline (no network)
// from the stored dependencies (T-6.6).
func (m *Manager) runCapsuleTest(ctx context.Context, b *artifact.Build, log io.Writer) error {
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	dir, err := artifact.CapsuleDir(m.Lib.Layout.Root, repo.R().Source)
	if err != nil {
		return err
	}
	version := b.Version
	if version == "" {
		version = b.Ref
	}
	var c *artifact.Capsule
	for _, x := range artifact.ReadCapsules(dir) {
		if x.Version == version {
			cc := x
			c = &cc
			break
		}
	}
	if c == nil {
		return fmt.Errorf("%w: %s", artifact.ErrNoCapsule, version)
	}
	b.Commit = c.Commit
	work := filepath.Join(b.Dir, "work")
	if err := m.checkout(ctx, b, work); err != nil {
		return err
	}
	files, err := artifact.ReadFiles(c.Path)
	if err != nil {
		return err
	}
	if err := artifact.BlobsOf(m.Lib.Layout.Root).Materialize(files, work); err != nil {
		return err
	}
	fmt.Fprintf(log, "Kapsel %s ausgepackt (%d Dateien). Baue ohne Netz …\n", version, len(files))
	var testErr error
	for _, e := range c.Ecosystems {
		if e.Build == "" {
			continue
		}
		fmt.Fprintf(log, "== %s: %s ==\n", e.Name, e.Build)
		args := append(m.containerArgs("reposhelf-captest-"+strings.ToLower(b.ID)+"-"+e.Name, work, false), e.Image, "sh", "-c", "set -e\n"+e.Build)
		if err := m.command(ctx, log, "", m.Docker, args...); err != nil {
			testErr = fmt.Errorf("%s: %w", e.Name, err)
			break
		}
	}
	status := artifact.StatusSuccess
	if testErr != nil {
		status = artifact.StatusFailed
	}
	c.Test = &artifact.TestResult{Status: status, Date: time.Now().UTC().Truncate(time.Second), BuildID: b.ID}
	if err := artifact.WriteCapsule(dir, *c, nil); err != nil {
		return err
	}
	return testErr
}

// Capsules lists the capsules of a repository.
func (m *Manager) Capsules(ctx context.Context, ref string) ([]artifact.Capsule, error) {
	repo, err := m.Lib.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	dir, err := artifact.CapsuleDir(m.Lib.Layout.Root, repo.R().Source)
	if err != nil {
		return nil, err
	}
	return artifact.ReadCapsules(dir), nil
}

// AutoCapsules queues a capsule for the newest version of every buildable
// repository that has none yet (called by the scheduler; such builds wait
// for confirmation when the runner requires it).
func (m *Manager) AutoCapsules(ctx context.Context) {
	if !m.Lib.Config().Runner.Enabled {
		return
	}
	entries, err := m.Lib.List(ctx, store.Query{})
	if err != nil {
		return
	}
	pending := map[string]bool{}
	for _, b := range artifact.ListBuilds(m.Lib.Layout.Root) {
		if b.Kind == artifact.KindCapsule && !b.Done() {
			pending[b.RepoID+"@"+b.Version] = true
		}
	}
	for _, e := range entries {
		if !e.Buildable {
			continue
		}
		repo, err := m.Lib.Get(ctx, e.ID)
		if err != nil {
			continue
		}
		vs, err := m.Lib.Git.Versions(ctx, repo.Paths.Mirror)
		if err != nil || len(vs) == 0 {
			continue
		}
		newest := vs[0].Name
		caps, _ := m.Capsules(ctx, e.ID)
		has := false
		for _, c := range caps {
			if c.Version == newest {
				has = true
			}
		}
		if has || pending[e.ID+"@"+newest] {
			continue
		}
		if _, err := m.StartBuild(ctx, StartRequest{Repo: e.ID, Ref: newest, Target: "capsule", Actor: "scheduler", Version: newest}); err != nil {
			m.Log.Warn("auto capsule", "repo", e.Key, "err", err)
		}
	}
}

var _ = core.ErrNotFound
