// SPDX-License-Identifier: GPL-3.0-or-later

// Package gitcli runs the system git (D-006) for RepoShelf: mirroring with
// force-push protection, reading files and trees, and serving (http-backend).
//
// Every call has a context for timeout and cancellation, never prompts for
// credentials, and passes names from the network only after "--" or as
// absolute paths, so a name like "--upload-pack=x" cannot become an option.
// Credentials go through environment variables (GIT_CONFIG_*), never through
// the command line, where other users could read them.
//
// It depends only on the standard library.
package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// MinVersion is the oldest git that RepoShelf supports (protocol v2 by
// default, "git init --initial-branch").
var MinVersion = [3]int{2, 28, 0}

// Errors callers distinguish.
var (
	ErrNotFound    = errors.New("git not found")
	ErrTooOld      = errors.New("git is too old")
	ErrUnreachable = errors.New("repository not reachable")
	ErrAuth        = errors.New("authentication required or refused")
	ErrGone        = errors.New("repository does not exist (anymore)")
)

// Git is a located git executable.
type Git struct {
	Path string
	// Env is added to every call (e.g. HOME for tests).
	Env []string
}

// Find locates git: first a portable Git next to the program (D-020:
// <exe dir>/git/cmd/git.exe on Windows, <exe dir>/git/bin/git elsewhere),
// then git in PATH.
func Find(exeDir string) (*Git, error) {
	if exeDir != "" {
		candidates := []string{filepath.Join(exeDir, "git", "bin", "git")}
		if runtime.GOOS == "windows" {
			candidates = []string{
				filepath.Join(exeDir, "git", "cmd", "git.exe"),
				filepath.Join(exeDir, "git", "mingw64", "bin", "git.exe"),
			}
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return &Git{Path: c}, nil
			}
		}
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNotFound
	}
	return &Git{Path: p}, nil
}

// Auth carries credentials for one remote. Token is sent as HTTP basic auth
// with the user "x-access-token" (GitHub, Gitea and GitLab accept it).
type Auth struct {
	Token string
	// URLPrefix limits the credentials to this URL prefix (scheme://host/).
	URLPrefix string
}

func (a *Auth) env() []string {
	if a == nil || a.Token == "" || a.URLPrefix == "" {
		return nil
	}
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + a.Token))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http." + a.URLPrefix + ".extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + cred,
	}
}

// Error is a failed git call with its (trimmed) stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git in dir and returns stdout.
func (g *Git) Run(ctx context.Context, dir string, auth *Auth, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := g.command(ctx, dir, auth, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), classify(&Error{Args: args, Stderr: stderr.String(), Err: err}, ctx)
	}
	return stdout.Bytes(), nil
}

// Command returns a prepared command (for streaming, e.g. http-backend).
func (g *Git) Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return g.command(ctx, dir, nil, args...)
}

func (g *Git) command(ctx context.Context, dir string, auth *Auth, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, g.Path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never wait for a password
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GCM_INTERACTIVE=never",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
		"LC_ALL=C", // stable, parseable messages
		"LANG=C",
	)
	cmd.Env = append(cmd.Env, g.Env...)
	cmd.Env = append(cmd.Env, auth.env()...)
	return cmd
}

var (
	reGone = regexp.MustCompile(`(?i)(repository '.*' not found|repository not found|does not appear to be a git repository|(error|http):? 404\b)`)
	reAuth = regexp.MustCompile(`(?i)(authentication failed|could not read username|terminal prompts disabled|permission denied \(publickey|(error|http):? 40[13]\b)`)
	reNet  = regexp.MustCompile(`(?i)(could not resolve host|connection (timed out|refused|reset)|failed to connect|network is unreachable|operation timed out|(error|http):? 5\d\d\b|early eof|\btls\b|\bssl\b)`)
)

// classify maps git's messages to sentinel errors that drive the upstream
// status (ok / degraded / gone).
func classify(e *Error, ctx context.Context) error {
	if ctx.Err() != nil {
		e.Err = fmt.Errorf("%w: %w", ErrUnreachable, ctx.Err())
		return e
	}
	switch {
	case reAuth.MatchString(e.Stderr):
		// GitHub answers "not found" for private repositories without
		// access as well; auth wins because a token may fix it.
		e.Err = fmt.Errorf("%w: %w", ErrAuth, e.Err)
	case reNet.MatchString(e.Stderr):
		e.Err = fmt.Errorf("%w: %w", ErrUnreachable, e.Err)
	case reGone.MatchString(e.Stderr):
		e.Err = fmt.Errorf("%w: %w", ErrGone, e.Err)
	}
	return e
}

// Version returns git's version as major, minor, patch.
func (g *Git) Version(ctx context.Context) ([3]int, string, error) {
	out, err := g.Run(ctx, "", nil, "version")
	if err != nil {
		return [3]int{}, "", err
	}
	text := strings.TrimSpace(string(out))
	m := regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`).FindStringSubmatch(text)
	if m == nil {
		return [3]int{}, text, fmt.Errorf("unknown git version %q", text)
	}
	var v [3]int
	for i := range 3 {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, text, nil
}

// Check verifies that git runs and is new enough.
func (g *Git) Check(ctx context.Context) (string, error) {
	v, text, err := g.Version(ctx)
	if err != nil {
		return text, err
	}
	for i := range 3 {
		if v[i] != MinVersion[i] {
			if v[i] < MinVersion[i] {
				return text, fmt.Errorf("%w: %s, need %d.%d", ErrTooOld, text, MinVersion[0], MinVersion[1])
			}
			break
		}
	}
	return text, nil
}

// LFSAvailable reports whether git-lfs is installed.
func (g *Git) LFSAvailable(ctx context.Context) bool {
	_, err := g.Run(ctx, "", nil, "lfs", "version")
	return err == nil
}

// Refs maps full ref names to object IDs.
type Refs map[string]string

func parseRefs(out []byte, sep string) Refs {
	refs := Refs{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		oid, name, ok := strings.Cut(sc.Text(), sep)
		if !ok || strings.HasSuffix(name, "^{}") {
			continue
		}
		refs[strings.TrimSpace(name)] = strings.TrimSpace(oid)
	}
	return refs
}

// LsRemote lists the remote's branches, tags and notes.
func (g *Git) LsRemote(ctx context.Context, url string, auth *Auth) (Refs, error) {
	out, err := g.Run(ctx, "", auth, "ls-remote", "--refs", "--", url, "refs/heads/*", "refs/tags/*", "refs/notes/*")
	if err != nil {
		return nil, err
	}
	return parseRefs(out, "\t"), nil
}

// RemoteHead returns the remote's default branch ("main"), or "".
func (g *Git) RemoteHead(ctx context.Context, url string, auth *Auth) string {
	out, err := g.Run(ctx, "", auth, "ls-remote", "--symref", "--", url, "HEAD")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			name, _, _ := strings.Cut(rest, "\t")
			return name
		}
	}
	return ""
}

// LocalRefs lists refs of a local repository, without RepoShelf's backups.
func (g *Git) LocalRefs(ctx context.Context, dir string) (Refs, error) {
	out, err := g.Run(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/tags", "refs/notes")
	if err != nil {
		return nil, err
	}
	return parseRefs(out, " "), nil
}

// InitBare creates an empty bare repository at dir with "origin" pointing to
// url and fetching branches, tags and notes. GitHub's refs/pull/* are left
// out on purpose: they are rewritten constantly and would flood backups.
func (g *Git) InitBare(ctx context.Context, dir, url string) error {
	if _, err := g.Run(ctx, "", nil, "init", "--bare", "--quiet", "--initial-branch=main", "--", dir); err != nil {
		return err
	}
	cmds := [][]string{
		{"config", "remote.origin.url", url},
		{"config", "--replace-all", "remote.origin.fetch", "refs/heads/*:refs/heads/*"},
		{"config", "--add", "remote.origin.fetch", "refs/tags/*:refs/tags/*"},
		{"config", "--add", "remote.origin.fetch", "refs/notes/*:refs/notes/*"},
		{"config", "remote.origin.tagOpt", "--no-tags"},
		// Keep unreachable objects of rewritten history for backups.
		{"config", "gc.reflogExpire", "never"},
		{"config", "gc.reflogExpireUnreachable", "never"},
		{"config", "core.logAllRefUpdates", "always"},
	}
	for _, c := range cmds {
		if _, err := g.Run(ctx, dir, nil, c...); err != nil {
			return err
		}
	}
	return nil
}

// SetURL changes the origin URL of a mirror.
func (g *Git) SetURL(ctx context.Context, dir, url string) error {
	_, err := g.Run(ctx, dir, nil, "config", "remote.origin.url", url)
	return err
}

// SetHead points HEAD of a bare repository to a branch.
func (g *Git) SetHead(ctx context.Context, dir, branch string) error {
	if branch == "" {
		return nil
	}
	_, err := g.Run(ctx, dir, nil, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	return err
}

// Head returns the branch HEAD points to.
func (g *Git) Head(ctx context.Context, dir string) string {
	out, err := g.Run(ctx, dir, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FixHead points HEAD to an existing branch when it dangles (e.g. after the
// first push of "master" into a repository initialised with "main").
func (g *Git) FixHead(ctx context.Context, dir string) {
	if g.HasCommits(ctx, dir) {
		return
	}
	branches, err := g.Branches(ctx, dir)
	if err != nil || len(branches) == 0 {
		return
	}
	pick := branches[0].Name
	for _, b := range branches {
		if b.Name == "main" || b.Name == "master" {
			pick = b.Name
			break
		}
	}
	g.SetHead(ctx, dir, pick)
}
