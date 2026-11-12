// SPDX-License-Identifier: GPL-3.0-or-later

package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// containerArgs are the common docker run options: limits from the
// configuration, no privileges, read-write mount of the checkout only.
func (m *Manager) containerArgs(name, src string, network bool) []string {
	cfg := m.Lib.Config().Runner
	args := []string{"run", "--rm", "--name", name, "--security-opt", "no-new-privileges", "--pids-limit", "4096",
		"-v", src + ":/src", "-w", "/src"}
	if cfg.MemoryLimit != "" {
		args = append(args, "--memory", cfg.MemoryLimit)
	}
	if cfg.CPULimit != "" {
		args = append(args, "--cpus", cfg.CPULimit)
	}
	if !network {
		args = append(args, "--network", "none")
	}
	return args
}

// runDocker builds a reposhelf-build.yaml target (T-7.3).
func (m *Manager) runDocker(ctx context.Context, b *artifact.Build, log io.Writer) error {
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	targets, err := Targets(ctx, m.Lib.Git, repo.Paths.Mirror, b.Commit, false)
	if err != nil {
		return err
	}
	var t *Target
	for i := range targets {
		if targets[i].Name == b.Target && targets[i].Kind == "docker" {
			t = &targets[i]
		}
	}
	if t == nil {
		return fmt.Errorf("%w: %s", ErrTarget, b.Target)
	}
	work := filepath.Join(b.Dir, "work")
	fmt.Fprintf(log, "Quelle auspacken (%s) …\n", short(b.Commit))
	if err := m.checkout(ctx, b, work); err != nil {
		return err
	}
	network := t.Network == nil || *t.Network
	args := m.containerArgs("reposhelf-build-"+strings.ToLower(b.ID), work, network)
	keys := make([]string, 0, len(t.Env))
	for k := range t.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+t.Env[k])
	}
	// A secret reaches the container only when the target asks for it AND
	// the user released it for this repository: the build definition is
	// part of the (possibly foreign) repository and must not decide alone
	// (KONZEPT 6.12, R-013). Values travel in docker's environment, not on
	// its command line (process list).
	var env []string
	for _, s := range t.Secrets {
		switch v, ok := m.LookupEnv("REPOSHELF_SECRET_" + s); {
		case !repometa.SecretName.MatchString(s) || !slices.Contains(repo.R().Buildable.Secrets, s):
			fmt.Fprintf(log, "Hinweis: Geheimnis %s ist für dieses Repo nicht freigegeben (Repo-Einstellungen → Baubar halten)\n", s)
		case !ok:
			fmt.Fprintf(log, "Hinweis: Geheimnis %s nicht gesetzt (REPOSHELF_SECRET_%s)\n", s, s)
		default:
			args = append(args, "-e", s)
			env = append(env, s+"="+v)
		}
	}
	script := "set -e\n" + strings.Join(t.Commands, "\n")
	args = append(args, t.Image, "sh", "-c", script)
	fmt.Fprintf(log, "docker run %s (Netz: %v)\n", t.Image, network)
	if err := m.commandEnv(ctx, log, "", env, m.Docker, args...); err != nil {
		return err
	}
	return m.collect(b, work, t.Artifacts, log)
}

// command runs an executable and streams its output into the log.
func (m *Manager) command(ctx context.Context, log io.Writer, dir, name string, args ...string) error {
	return m.commandEnv(ctx, log, dir, nil, name, args...)
}

// commandEnv is command with extra environment variables.
func (m *Manager) commandEnv(ctx context.Context, log io.Writer, dir string, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	gitcli.HideWindow(cmd)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() { stream(pr, log); close(done) }()
	err := cmd.Run()
	_ = pw.Close()
	<-done
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return nil
}

// collect copies files matching patterns from work into artifacts/.
func (m *Manager) collect(b *artifact.Build, work string, patterns []string, log io.Writer) error {
	dst := filepath.Join(b.Dir, artifact.ArtifactsDir)
	for _, pat := range patterns {
		matches, err := filepath.Glob(filepath.Join(work, filepath.FromSlash(pat)))
		if err != nil {
			return err
		}
		for _, src := range matches {
			_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !d.Type().IsRegular() {
					return nil
				}
				rel, _ := filepath.Rel(work, p)
				f, err := copyHashed(p, filepath.Join(dst, rel))
				if err == nil {
					f.Name = filepath.ToSlash(rel)
					b.Artifacts = append(b.Artifacts, f)
					fmt.Fprintf(log, "Artefakt: %s (%d Bytes)\n", f.Name, f.Size)
				}
				return nil
			})
		}
	}
	if len(patterns) > 0 && len(b.Artifacts) == 0 {
		fmt.Fprintln(log, "Hinweis: keine Artefakte gefunden")
	}
	return nil
}

func copyHashed(src, dst string) (artifact.File, error) {
	in, err := os.Open(src)
	if err != nil {
		return artifact.File{}, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return artifact.File{}, err
	}
	out, err := os.Create(dst)
	if err != nil {
		return artifact.File{}, err
	}
	defer out.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	return artifact.File{Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

// runAct runs a GitHub workflow job locally with act (T-7.2). act needs a
// git checkout, so the mirror is cloned (local, cheap).
func (m *Manager) runAct(ctx context.Context, b *artifact.Build, log io.Writer) error {
	if m.Act == "" {
		return fmt.Errorf("act ist nicht installiert")
	}
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	work := filepath.Join(b.Dir, "work")
	if _, err := m.Lib.Git.Run(ctx, "", nil, "clone", "--quiet", "--no-checkout", "--", repo.Paths.Mirror, work); err != nil {
		return err
	}
	if _, err := m.Lib.Git.Run(ctx, work, nil, "checkout", "--quiet", "--detach", b.Commit); err != nil {
		return err
	}
	wfFile, job, _ := strings.Cut(strings.TrimPrefix(b.Target, "act:"), "/")
	workflow := ""
	targets, _ := Targets(ctx, m.Lib.Git, repo.Paths.Mirror, b.Commit, false)
	for _, t := range targets {
		if t.Name == b.Target {
			workflow, job = t.Workflow, t.Job
		}
	}
	if workflow == "" {
		return fmt.Errorf("%w: %s", ErrTarget, wfFile)
	}
	arts := filepath.Join(b.Dir, artifact.ArtifactsDir)
	if err := os.MkdirAll(arts, 0o755); err != nil {
		return err
	}
	cfg := m.Lib.Config().Runner
	opts := "--security-opt no-new-privileges"
	if cfg.MemoryLimit != "" {
		opts += " --memory " + cfg.MemoryLimit
	}
	if cfg.CPULimit != "" {
		opts += " --cpus " + cfg.CPULimit
	}
	args := []string{"-W", workflow, "-j", job, "--artifact-server-path", arts, "--container-options", opts,
		"-P", "ubuntu-latest=catthehacker/ubuntu:act-latest", "--rm"}
	fmt.Fprintf(log, "act %s, Job %s\n", workflow, job)
	if err := m.command(ctx, log, work, m.Act, args...); err != nil {
		return err
	}
	_ = filepath.WalkDir(arts, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(arts, p)
			info, _ := d.Info()
			sum, _ := fileSum(p)
			b.Artifacts = append(b.Artifacts, artifact.File{Name: filepath.ToSlash(rel), Size: info.Size(), SHA256: sum})
		}
		return nil
	})
	return nil
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

// ---- GitHub Actions as external runner (T-7.4) ----

func (m *Manager) githubAPI(ctx context.Context, method, path string, body any, out any) (*http.Response, error) {
	base := "https://api.github.com"
	if b, ok := m.Lib.Sources.BaseURLs["github.com"]; ok {
		base = strings.TrimSuffix(b, "/")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	u := path
	if !strings.HasPrefix(u, "http") {
		u = base + path
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+m.Lib.Sources.Tokens("github.com"))
	req.Header.Set("User-Agent", "RepoShelf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return resp, fmt.Errorf("GitHub %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		defer resp.Body.Close()
		return resp, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp, nil
}

// githubPoll is how often a dispatched run is checked.
var githubPoll = 20 * time.Second

func (m *Manager) runGitHub(ctx context.Context, b *artifact.Build, log io.Writer) error {
	if m.Lib.Sources.Tokens("github.com") == "" {
		return fmt.Errorf("kein GitHub-Token (REPOSHELF_GITHUB_TOKEN) – für GitHub Actions nötig")
	}
	repo, err := m.Lib.Get(ctx, b.RepoID)
	if err != nil {
		return err
	}
	src := repo.R().Source
	if src.HostName() != "github.com" {
		return fmt.Errorf("GitHub Actions gibt es nur für Repos auf github.com")
	}
	workflow := strings.TrimPrefix(b.Target, "github:")
	ref := b.Ref
	if ref == "HEAD" {
		ref = repo.Entry.DefaultBranch
		if ref == "" {
			ref = "main"
		}
	}
	rp := "/repos/" + url.PathEscape(src.Owner) + "/" + url.PathEscape(src.Name)
	start := time.Now().UTC().Add(-5 * time.Second)
	if _, err := m.githubAPI(ctx, http.MethodPost, rp+"/actions/workflows/"+url.PathEscape(workflow)+"/dispatches", map[string]string{"ref": ref}, nil); err != nil {
		return err
	}
	fmt.Fprintf(log, "Workflow %s auf GitHub angestoßen (Ref %s) – warte auf den Lauf …\n", workflow, ref)
	var run struct {
		ID         int64  `json:"id"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	}
	for run.ID == 0 {
		var list struct {
			Runs []struct {
				ID         int64     `json:"id"`
				Status     string    `json:"status"`
				Conclusion string    `json:"conclusion"`
				HTMLURL    string    `json:"html_url"`
				Created    time.Time `json:"created_at"`
			} `json:"workflow_runs"`
		}
		if _, err := m.githubAPI(ctx, http.MethodGet, rp+"/actions/workflows/"+url.PathEscape(workflow)+"/runs?event=workflow_dispatch&per_page=5", nil, &list); err != nil {
			return err
		}
		for _, r := range list.Runs {
			if !r.Created.Before(start) {
				run.ID, run.HTMLURL = r.ID, r.HTMLURL
			}
		}
		if run.ID == 0 {
			if err := sleep(ctx, githubPoll/4); err != nil {
				return err
			}
		}
	}
	b.External = run.HTMLURL
	m.save(*b)
	fmt.Fprintf(log, "Lauf: %s\n", run.HTMLURL)
	for run.Status != "completed" {
		if err := sleep(ctx, githubPoll); err != nil {
			return err
		}
		if _, err := m.githubAPI(ctx, http.MethodGet, fmt.Sprintf("%s/actions/runs/%d", rp, run.ID), nil, &run); err != nil {
			return err
		}
		fmt.Fprintf(log, "Status: %s\n", run.Status)
	}
	if run.Conclusion != "success" {
		return fmt.Errorf("GitHub-Lauf beendet mit %q", run.Conclusion)
	}
	var arts struct {
		Artifacts []struct {
			Name string `json:"name"`
			URL  string `json:"archive_download_url"`
			Size int64  `json:"size_in_bytes"`
		} `json:"artifacts"`
	}
	if _, err := m.githubAPI(ctx, http.MethodGet, fmt.Sprintf("%s/actions/runs/%d/artifacts", rp, run.ID), nil, &arts); err != nil {
		return err
	}
	dst := filepath.Join(b.Dir, artifact.ArtifactsDir)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, a := range arts.Artifacts {
		resp, err := m.githubAPI(ctx, http.MethodGet, a.URL, nil, nil)
		if err != nil {
			return err
		}
		name := safeName(a.Name) + ".zip"
		p := filepath.Join(dst, name)
		f, err := os.Create(p)
		if err != nil {
			resp.Body.Close()
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
		resp.Body.Close()
		f.Close()
		if err != nil {
			return err
		}
		if _, err := zip.OpenReader(p); err != nil {
			fmt.Fprintf(log, "Hinweis: %s ist kein gültiges ZIP\n", name)
		}
		b.Artifacts = append(b.Artifacts, artifact.File{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		fmt.Fprintf(log, "Artefakt ins Archiv übernommen: %s (%d Bytes)\n", name, n)
	}
	return nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(s string) string {
	s = unsafeChars.ReplaceAllString(s, "_")
	if s == "" || s == "." || s == ".." {
		s = "artefakt"
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// forward hands a build to a paired instance (Phase 8).
func (m *Manager) forward(b artifact.Build) {
	m.mu.Lock()
	var r Remote
	for _, x := range m.remotes {
		if x.Name() == b.Runner {
			r = x
		}
	}
	m.mu.Unlock()
	now := time.Now().UTC()
	if r == nil {
		b.Status, b.Error, b.Finished = artifact.StatusFailed, "Runner "+b.Runner+" unbekannt", &now
		m.save(b)
		return
	}
	b.Status, b.Started = artifact.StatusRunning, &now
	m.save(b)
	ext, err := r.Start(context.Background(), b)
	b.External = ext
	fin := time.Now().UTC()
	b.Finished = &fin
	if err != nil {
		b.Status, b.Error = artifact.StatusFailed, err.Error()
	} else {
		b.Status = artifact.StatusSuccess
	}
	m.save(b)
}

// TargetNames lists buildable targets of a repository at ref (UI, MCP).
func (m *Manager) TargetNames(ctx context.Context, ref string) []string {
	repo, err := m.Lib.Get(ctx, ref)
	if err != nil || !m.Lib.Git.HasCommits(ctx, repo.Paths.Mirror) {
		return nil
	}
	ts, err := Targets(ctx, m.Lib.Git, repo.Paths.Mirror, "HEAD", repo.Entry.Host == "github.com")
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	if m.hasDockerfile(ctx, repo.Paths.Mirror, "HEAD") {
		out = append(out, TargetImage)
	}
	return out
}

var _ = yaml.Marshal
