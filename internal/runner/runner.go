// SPDX-License-Identifier: GPL-3.0-or-later

// Package runner executes builds and creates build capsules (KONZEPT 6.11,
// 6.12, Phases 6 and 7). Foreign code only ever runs inside containers
// (D-008): either `docker run` with resource limits and – unless a target
// asks for it – without network, or `act` for GitHub workflows, or remotely
// on GitHub Actions. Secrets are only passed when a target names them.
//
// Build records, logs and artifacts are plain files below builds/ (package
// artifact). The runner depends on core, never the other way round.
package runner

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/fsinfo"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Errors.
var (
	ErrDisabled = errors.New("this instance has no runner role")
	ErrNoDocker = errors.New("docker is not available")
	ErrNoSpace  = errors.New("not enough free disk space")
	ErrTarget   = errors.New("unknown build target")
	ErrState    = errors.New("build is not in a state that allows this")
)

// Runner names.
const (
	RunnerLocal  = "local"
	RunnerGitHub = "github"
)

// Remote forwards builds to another instance (Phase 8 pairing).
type Remote interface {
	Name() string
	Capabilities() []string
	Online(ctx context.Context) bool
	Start(ctx context.Context, b artifact.Build) (string, error)
}

// Manager runs builds of one library.
type Manager struct {
	Lib *core.Library
	Log *slog.Logger
	// Docker and Act are executables; empty = look up in PATH.
	Docker, Act string
	// LookupEnv gives access to REPOSHELF_SECRET_* (explicit secrets).
	LookupEnv func(string) (string, bool)

	mu      sync.Mutex
	running map[string]context.CancelFunc
	queue   chan string
	remotes []Remote
	once    sync.Once
}

// New creates a manager and starts its worker.
func New(lib *core.Library, log *slog.Logger) *Manager {
	m := &Manager{Lib: lib, Log: log, LookupEnv: os.LookupEnv, running: map[string]context.CancelFunc{}, queue: make(chan string, 256)}
	if p, err := exec.LookPath("docker"); err == nil {
		m.Docker = p
	}
	if p, err := exec.LookPath("act"); err == nil {
		m.Act = p
	}
	return m
}

// AddRemote registers a paired instance with runner role.
func (m *Manager) AddRemote(r Remote) {
	m.mu.Lock()
	m.remotes = append(m.remotes, r)
	m.mu.Unlock()
}

// Start launches the worker loop; builds are processed one after another
// per configured parallelism.
func (m *Manager) Start(ctx context.Context) {
	m.once.Do(func() {
		par := max(1, m.Lib.Config().Runner.MaxParallel)
		for range par {
			go m.worker(ctx)
		}
		// Resume queued builds after a restart; running ones were interrupted.
		for _, b := range artifact.ListBuilds(m.Lib.Layout.Root) {
			switch b.Status {
			case artifact.StatusQueued:
				m.queue <- b.Dir
			case artifact.StatusRunning:
				b.Status, b.Error = artifact.StatusFailed, "durch Neustart abgebrochen"
				now := time.Now().UTC()
				b.Finished = &now
				m.save(b)
			}
		}
	})
}

// RunnerInfo describes a runner for the UI (Builds & Runner).
type RunnerInfo struct {
	Name, Label  string
	Online       bool
	Capabilities []string
	Note         string
	Busy, Slots  int
}

// Runners lists the available runners.
func (m *Manager) Runners(ctx context.Context) []RunnerInfo {
	cfg := m.Lib.Config()
	var out []RunnerInfo
	local := RunnerInfo{Name: RunnerLocal, Label: cfg.Instance.Name + " (diese Instanz)", Capabilities: cfg.Runner.Capabilities, Slots: max(1, cfg.Runner.MaxParallel)}
	switch {
	case !cfg.Runner.Enabled:
		local.Note = "Runner-Rolle aus (Einstellungen)"
	case m.dockerOK(ctx) != nil:
		local.Note = "Docker nicht erreichbar"
	default:
		local.Online = true
		if cfg.Runner.Confirm {
			local.Note = "jeder Build braucht deine Bestätigung"
		}
		if free := fsinfo.FreeBytes(m.Lib.Layout.Root); free >= 0 {
			local.Note = strings.TrimPrefix(local.Note+fmt.Sprintf(" · %.1f GB frei", float64(free)/(1<<30)), " · ")
		}
	}
	m.mu.Lock()
	local.Busy = len(m.running)
	remotes := slices.Clone(m.remotes)
	m.mu.Unlock()
	out = append(out, local)
	gh := RunnerInfo{Name: RunnerGitHub, Label: "GitHub Actions", Capabilities: []string{"linux", "macos", "windows"}, Note: "für Repos auf GitHub · Artefakte werden ins Archiv übernommen"}
	if m.Lib.Sources.Tokens("github.com") != "" {
		gh.Online = true
	} else {
		gh.Note = "kein GitHub-Token gesetzt"
	}
	out = append(out, gh)
	for _, r := range remotes {
		out = append(out, RunnerInfo{Name: r.Name(), Label: r.Name(), Online: r.Online(ctx), Capabilities: r.Capabilities(), Note: "gekoppelte Instanz"})
	}
	return out
}

func (m *Manager) dockerOK(ctx context.Context) error {
	if m.Docker == "" {
		return ErrNoDocker
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, m.Docker, "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		return fmt.Errorf("%w: %v", ErrNoDocker, err)
	}
	return nil
}

// StartRequest describes a build to start.
type StartRequest struct {
	Repo    string
	Ref     string
	Target  string
	Runner  string // "" = automatic
	Actor   string
	Confirm bool // the user started it explicitly in the UI
	Version string
}

// Start creates a build record and queues it. Builds that need a
// confirmation wait in StatusWaiting (T-7.5).
func (m *Manager) StartBuild(ctx context.Context, req StartRequest) (artifact.Build, error) {
	repo, err := m.Lib.Get(ctx, req.Repo)
	if err != nil {
		return artifact.Build{}, err
	}
	ref := req.Ref
	if ref == "" {
		ref = "HEAD"
	}
	out, err := m.Lib.Git.Run(ctx, repo.Paths.Mirror, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return artifact.Build{}, fmt.Errorf("%w: %s", core.ErrNotFound, ref)
	}
	commit := strings.TrimSpace(string(out))
	kind, target, err := m.resolveTarget(ctx, repo, commit, req.Target)
	if err != nil {
		return artifact.Build{}, err
	}
	runner := req.Runner
	if runner == "" {
		runner = RunnerLocal
		if kind == artifact.KindGitHub {
			runner = RunnerGitHub
		}
	}
	dir, err := artifact.BuildsDir(m.Lib.Layout.Root, repo.R().Source)
	if err != nil {
		return artifact.Build{}, err
	}
	now := m.Lib.Now().UTC().Truncate(time.Second)
	id := repometa.NewID(now)
	b := artifact.Build{ID: id, RepoID: repo.R().ID, Key: repo.Entry.Key, Ref: ref, Commit: commit, Target: target, Kind: kind,
		Runner: runner, Status: artifact.StatusQueued, Actor: req.Actor, Created: now, Dir: filepath.Join(dir, id), Version: req.Version}
	cfg := m.Lib.Config().Runner
	if runner == RunnerLocal && !cfg.Enabled {
		return artifact.Build{}, ErrDisabled
	}
	if runner == RunnerLocal && cfg.Confirm && !req.Confirm {
		b.Status = artifact.StatusWaiting
	}
	if err := artifact.WriteBuild(b); err != nil {
		return artifact.Build{}, err
	}
	m.Lib.Record(core.Activity{Actor: req.Actor, Action: "build_start", RepoID: b.RepoID, Detail: b.Target + " @ " + b.Ref})
	if b.Status == artifact.StatusQueued {
		m.enqueue(b)
	}
	return b, nil
}

func (m *Manager) enqueue(b artifact.Build) {
	if b.Runner != RunnerLocal && b.Runner != RunnerGitHub {
		go m.forward(b)
		return
	}
	select {
	case m.queue <- b.Dir:
	default:
		m.Log.Warn("build queue full", "build", b.ID)
	}
}

func (m *Manager) resolveTarget(ctx context.Context, repo *core.Repo, commit, name string) (string, string, error) {
	switch name {
	case artifact.KindCapsule:
		return artifact.KindCapsule, "capsule", nil
	case "capsule-test", artifact.KindCapsuleTest:
		return artifact.KindCapsuleTest, "capsule-test", nil
	}
	targets, err := Targets(ctx, m.Lib.Git, repo.Paths.Mirror, commit, repo.Entry.Host == "github.com")
	if err != nil {
		return "", "", err
	}
	if name == "" && len(targets) > 0 {
		name = targets[0].Name
	}
	for _, t := range targets {
		if t.Name == name {
			kind := map[string]string{"docker": artifact.KindDocker, "act": artifact.KindAct, "github": artifact.KindGitHub}[t.Kind]
			return kind, t.Name, nil
		}
	}
	return "", "", fmt.Errorf("%w: %q (in %s, .github/workflows oder „capsule“)", ErrTarget, name, DefinitionFile)
}

// Confirm releases a waiting build.
func (m *Manager) Confirm(id, actor string) error {
	b, err := m.Get(id)
	if err != nil {
		return err
	}
	if b.Status != artifact.StatusWaiting {
		return ErrState
	}
	b.Status = artifact.StatusQueued
	if err := artifact.WriteBuild(b); err != nil {
		return err
	}
	m.Lib.Record(core.Activity{Actor: actor, Action: "build_confirm", RepoID: b.RepoID, Detail: b.Target})
	m.enqueue(b)
	return nil
}

// Cancel stops a waiting, queued or running build.
func (m *Manager) Cancel(id, actor string) error {
	b, err := m.Get(id)
	if err != nil {
		return err
	}
	if b.Done() {
		return ErrState
	}
	m.mu.Lock()
	cancel := m.running[b.ID]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	b.Status = artifact.StatusCancelled
	now := time.Now().UTC()
	b.Finished = &now
	m.Lib.Record(core.Activity{Actor: actor, Action: "build_cancel", RepoID: b.RepoID, Detail: b.Target})
	return artifact.WriteBuild(b)
}

// List returns builds, optionally of one repository.
func (m *Manager) List(repoID string, limit int) []artifact.Build {
	var out []artifact.Build
	for _, b := range artifact.ListBuilds(m.Lib.Layout.Root) {
		if repoID == "" || b.RepoID == repoID {
			out = append(out, b)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Get returns one build.
func (m *Manager) Get(id string) (artifact.Build, error) {
	for _, b := range artifact.ListBuilds(m.Lib.Layout.Root) {
		if b.ID == id {
			return b, nil
		}
	}
	return artifact.Build{}, fmt.Errorf("build %s: %w", id, core.ErrNotFound)
}

// BuildLog returns the build log.
func (m *Manager) BuildLog(id string) (string, error) {
	b, err := m.Get(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(b.Dir, artifact.LogFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// ArtifactPath returns a stored artifact file.
func (m *Manager) ArtifactPath(id, name string) (string, error) {
	b, err := m.Get(id)
	if err != nil {
		return "", err
	}
	for _, a := range b.Artifacts {
		if a.Name == name {
			p := filepath.Join(b.Dir, artifact.ArtifactsDir, filepath.FromSlash(name))
			if !filepath.IsLocal(filepath.FromSlash(name)) {
				return "", core.ErrInvalid
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: %w", name, core.ErrNotFound)
}

// pollQueued is how often builds queued by other processes (e.g. the MCP
// stdio server started by an agent) are picked up.
var pollQueued = 30 * time.Second

func (m *Manager) worker(ctx context.Context) {
	tick := time.NewTicker(pollQueued)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, b := range artifact.ListBuilds(m.Lib.Layout.Root) {
				if b.Status == artifact.StatusQueued && (b.Runner == RunnerLocal || b.Runner == RunnerGitHub) {
					select {
					case m.queue <- b.Dir:
					default:
					}
				}
			}
		case dir := <-m.queue:
			b, err := artifact.ReadBuild(dir)
			if err != nil || b.Status != artifact.StatusQueued || !m.claim(b.ID) {
				continue
			}
			m.run(ctx, b)
		}
	}
}

// claim marks a build as taken by this process (one worker per build).
func (m *Manager) claim(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.running[id]; ok {
		return false
	}
	m.running[id] = func() {}
	return true
}

// buildTimeout bounds one build.
const buildTimeout = 3 * time.Hour

func (m *Manager) run(parent context.Context, b artifact.Build) {
	ctx, cancel := context.WithTimeout(parent, buildTimeout)
	defer cancel()
	m.mu.Lock()
	m.running[b.ID] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.running, b.ID)
		m.mu.Unlock()
	}()

	now := time.Now().UTC().Truncate(time.Second)
	b.Status, b.Started = artifact.StatusRunning, &now
	m.save(b)
	logf, err := os.OpenFile(filepath.Join(b.Dir, artifact.LogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		m.finish(b, err)
		return
	}
	defer logf.Close()
	lw := &lineWriter{w: logf}
	fmt.Fprintf(lw, "RepoShelf-Build %s · %s @ %s (%s) · Runner %s\n", b.ID, b.Key, b.Ref, short(b.Commit), b.Runner)

	switch b.Kind {
	case artifact.KindGitHub:
		err = m.runGitHub(ctx, &b, lw)
	default:
		if err = m.preflight(ctx); err == nil {
			switch b.Kind {
			case artifact.KindDocker:
				err = m.runDocker(ctx, &b, lw)
			case artifact.KindAct:
				err = m.runAct(ctx, &b, lw)
			case artifact.KindCapsule:
				err = m.runCapsule(ctx, &b, lw)
			case artifact.KindCapsuleTest:
				err = m.runCapsuleTest(ctx, &b, lw)
			default:
				err = fmt.Errorf("%w: kind %s", ErrTarget, b.Kind)
			}
		}
	}
	if ctx.Err() != nil && parent.Err() == nil {
		if cur, e := artifact.ReadBuild(b.Dir); e == nil && cur.Status == artifact.StatusCancelled {
			fmt.Fprintln(lw, "Abgebrochen.")
			return
		}
	}
	if err != nil {
		fmt.Fprintf(lw, "FEHLER: %v\n", err)
	} else {
		fmt.Fprintln(lw, "Fertig.")
	}
	m.finish(b, err)
}

// preflight checks docker and free space (T-7.5: weak runners).
func (m *Manager) preflight(ctx context.Context) error {
	if err := m.dockerOK(ctx); err != nil {
		return err
	}
	min := int64(m.Lib.Config().Runner.MinFreeGB) << 30
	if free := fsinfo.FreeBytes(m.Lib.Layout.Root); free >= 0 && free < min {
		return fmt.Errorf("%w: %.1f GB frei, mindestens %d GB nötig", ErrNoSpace, float64(free)/(1<<30), m.Lib.Config().Runner.MinFreeGB)
	}
	return nil
}

func (m *Manager) finish(b artifact.Build, err error) {
	cur, rerr := artifact.ReadBuild(b.Dir)
	if rerr == nil && cur.Status == artifact.StatusCancelled {
		return
	}
	// Remove our own checkout before the build is reported as finished,
	// so nobody sees a "done" build that still occupies space.
	_ = os.RemoveAll(filepath.Join(b.Dir, "work"))
	now := time.Now().UTC().Truncate(time.Second)
	b.Finished = &now
	b.Status = artifact.StatusSuccess
	if err != nil {
		b.Status, b.Error = artifact.StatusFailed, err.Error()
	}
	if werr := artifact.WriteBuild(b); werr != nil {
		m.Log.Warn("write build", "err", werr)
	}
	m.Lib.Record(core.Activity{Actor: "runner", Action: "build_" + b.Status, RepoID: b.RepoID, Detail: b.Target})
}

// checkout extracts the source at b.Commit into dir (git archive).
func (m *Manager) checkout(ctx context.Context, b *artifact.Build, dir string) error {
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cmd := m.Lib.Git.Command(ctx, repo.Paths.Mirror, "archive", "--format=tar", "--end-of-options", b.Commit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := untar(stdout, dir); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

// untar extracts a tar stream below dir, refusing paths that escape it.
func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("unsafe path in archive: %q", h.Name)
		}
		p := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Links inside the checkout are kept only if they stay inside.
			target := filepath.Join(filepath.Dir(p), h.Linkname)
			if rel, err := filepath.Rel(dir, target); err != nil || !filepath.IsLocal(rel) {
				continue
			}
			_ = os.Symlink(h.Linkname, p) // links are optional for builds
		}
	}
}

// lineWriter is a synchronised log writer.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// stream copies a command's output into the log, line by line.
func stream(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		fmt.Fprintln(w, sc.Text())
	}
}

func short(s string) string {
	if len(s) > 9 {
		return s[:9]
	}
	return s
}

// save writes a build record; failures are logged (the build itself
// continues, the next write may succeed).
func (m *Manager) save(b artifact.Build) {
	if err := artifact.WriteBuild(b); err != nil {
		m.Log.Warn("write build record", "build", b.ID, "err", err)
	}
}
