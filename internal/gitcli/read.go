// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrTooLarge is returned by ReadFile for files above the limit.
var ErrTooLarge = errors.New("file too large")

// ErrBadRev is returned for revisions or paths that could be taken as
// options or escape the tree.
var ErrBadRev = errors.New("invalid revision or path")

// checkRev rejects revisions that start with "-" (option injection) or
// contain characters git never allows in names.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, " \t\n\x00:") {
		return fmt.Errorf("%w: %q", ErrBadRev, rev)
	}
	return nil
}

// cleanPath normalises a path inside a tree; "" is the root.
func cleanPath(p string) (string, error) {
	p = strings.Trim(path.Clean("/"+p), "/")
	if strings.ContainsAny(p, "\x00\n") {
		return "", fmt.Errorf("%w: %q", ErrBadRev, p)
	}
	return p, nil
}

// TreeEntry is one item of a directory listing.
type TreeEntry struct {
	Name string
	Path string
	Type string // blob, tree, commit (submodule)
	Size int64
}

// ListTree lists the directory dirPath at rev.
func (g *Git) ListTree(ctx context.Context, repo, rev, dirPath string) ([]TreeEntry, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	p, err := cleanPath(dirPath)
	if err != nil {
		return nil, err
	}
	spec := rev
	if p != "" {
		spec = rev + ":" + p
	}
	out, err := g.Run(ctx, repo, nil, "ls-tree", "-l", "-z", "--end-of-options", spec)
	if err != nil {
		return nil, err
	}
	var list []TreeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, name, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 4 {
			continue
		}
		size, _ := strconv.ParseInt(f[3], 10, 64)
		list = append(list, TreeEntry{Name: name, Path: strings.TrimPrefix(p+"/"+name, "/"), Type: f[1], Size: size})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if (list[i].Type == "tree") != (list[j].Type == "tree") {
			return list[i].Type == "tree"
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// ReadFile returns the content of filePath at rev, up to limit bytes.
func (g *Git) ReadFile(ctx context.Context, repo, rev, filePath string, limit int64) ([]byte, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	p, err := cleanPath(filePath)
	if err != nil || p == "" {
		return nil, fmt.Errorf("%w: %q", ErrBadRev, filePath)
	}
	spec := rev + ":" + p
	out, err := g.Run(ctx, repo, nil, "cat-file", "-s", "--end-of-options", spec)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, fs.ErrNotExist)
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if limit > 0 && size > limit {
		return nil, fmt.Errorf("%s (%d bytes): %w", p, size, ErrTooLarge)
	}
	return g.Run(ctx, repo, nil, "cat-file", "blob", "--end-of-options", spec)
}

// readmeNames in order of preference.
var readmeNames = []string{"readme.md", "readme.markdown", "readme", "readme.txt", "readme.rst", "readme.org"}

// Readme finds and reads the README in the root of rev. name is "" if
// there is none.
func (g *Git) Readme(ctx context.Context, repo, rev string, limit int64) (name string, content []byte, err error) {
	entries, err := g.ListTree(ctx, repo, rev, "")
	if err != nil {
		return "", nil, err
	}
	byLower := map[string]string{}
	for _, e := range entries {
		if e.Type == "blob" {
			byLower[strings.ToLower(e.Name)] = e.Name
		}
	}
	for _, n := range readmeNames {
		if real, ok := byLower[n]; ok {
			b, err := g.ReadFile(ctx, repo, rev, real, limit)
			return real, b, err
		}
	}
	return "", nil, nil
}

// HasCommits reports whether HEAD of repo resolves.
func (g *Git) HasCommits(ctx context.Context, repo string) bool {
	_, err := g.Run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return err == nil
}

// Commit is a log entry.
type Commit struct {
	Hash, Author, Subject string
	Time                  time.Time
}

// Log returns the newest n commits of rev.
func (g *Git) Log(ctx context.Context, repo, rev string, n int) ([]Commit, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	out, err := g.Run(ctx, repo, nil, "log", "-n", strconv.Itoa(n), "--format=%H%x00%an%x00%aI%x00%s", "--end-of-options", rev, "--")
	if err != nil {
		return nil, err
	}
	var list []Commit
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\x00")
		if len(f) != 4 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[2])
		list = append(list, Commit{Hash: f[0], Author: f[1], Time: t, Subject: f[3]})
	}
	return list, nil
}

// Ref is a branch or version with its commit time.
type Ref struct {
	Name string // short name
	OID  string
	Time time.Time
}

// Branches lists branches, newest commit first.
func (g *Git) Branches(ctx context.Context, repo string) ([]Ref, error) {
	return g.refList(ctx, repo, "refs/heads", "-committerdate")
}

// Versions lists tags (versions), newest first.
func (g *Git) Versions(ctx context.Context, repo string) ([]Ref, error) {
	return g.refList(ctx, repo, "refs/tags", "-creatordate")
}

func (g *Git) refList(ctx context.Context, repo, prefix, sortKey string) ([]Ref, error) {
	out, err := g.Run(ctx, repo, nil, "for-each-ref", "--sort="+sortKey, "--format=%(refname:short)%00%(objectname)%00%(creatordate:iso-strict)", prefix)
	if err != nil {
		return nil, err
	}
	var list []Ref
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\x00")
		if len(f) != 3 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[2])
		list = append(list, Ref{Name: f[0], OID: f[1], Time: t})
	}
	return list, nil
}

// DirSize sums the sizes of all files below dir.
func DirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// GrepHit is one matching line.
type GrepHit struct {
	Path string
	Line int
	Text string
}

// ErrBadPattern is returned for an empty or invalid search pattern.
var ErrBadPattern = errors.New("invalid search pattern")

// Grep searches the tree of rev (text files only). fixed searches the
// literal string; otherwise pattern is an extended regular expression.
// maxPerFile bounds matches per file.
func (g *Git) Grep(ctx context.Context, repo, rev, pattern string, fixed, ignoreCase bool, maxPerFile int) ([]GrepHit, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	if strings.TrimSpace(pattern) == "" || strings.ContainsAny(pattern, "\x00\n") {
		return nil, ErrBadPattern
	}
	args := []string{"grep", "-I", "-n", "-z", "--no-color", "--full-name", "--max-count", strconv.Itoa(max(1, maxPerFile))}
	if fixed {
		args = append(args, "-F")
	} else {
		// Check the expression first: git's messages are hard to classify.
		if _, err := regexp.Compile(pattern); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadPattern, err)
		}
		args = append(args, "-E")
	}
	if ignoreCase {
		args = append(args, "-i")
	}
	args = append(args, "-e", pattern, "--end-of-options", rev, "--")
	out, err := g.Run(ctx, repo, nil, args...)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.TrimSpace(ge.Stderr) == "" {
			return nil, nil // exit status 1: no match
		}
		if errors.As(err, &ge) && (strings.Contains(ge.Stderr, "regex") || strings.Contains(ge.Stderr, "command line")) {
			return nil, fmt.Errorf("%w: %s", ErrBadPattern, strings.TrimSpace(ge.Stderr))
		}
		return nil, err
	}
	var hits []GrepHit
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		f := strings.SplitN(line, "\x00", 3)
		if len(f) != 3 {
			continue
		}
		n, _ := strconv.Atoi(f[1])
		text := f[2]
		const maxLine = 400
		if len(text) > maxLine {
			text = text[:maxLine] + "…"
		}
		hits = append(hits, GrepHit{Path: strings.TrimPrefix(f[0], rev+":"), Line: n, Text: text})
	}
	return hits, nil
}

// TreeFile is a file of a whole tree (recursive listing).
type TreeFile struct {
	Path string
	Blob string
	Mode string // 100644, 100755, 120000 (symlink), 160000 (submodule)
	Size int64
}

// TreeFiles lists every file of rev, recursively.
func (g *Git) TreeFiles(ctx context.Context, repo, rev string) ([]TreeFile, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	out, err := g.Run(ctx, repo, nil, "ls-tree", "-r", "-l", "-z", "--end-of-options", rev)
	if err != nil {
		return nil, err
	}
	var list []TreeFile
	for _, rec := range bytes.Split(out, []byte{0}) {
		meta, name, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 4 {
			continue
		}
		size, _ := strconv.ParseInt(f[3], 10, 64)
		list = append(list, TreeFile{Path: name, Blob: f[2], Mode: f[0], Size: size})
	}
	return list, nil
}

// ReadBlob returns a blob by its ID, up to limit bytes.
func (g *Git) ReadBlob(ctx context.Context, repo, blob string, limit int64) ([]byte, error) {
	if !blobIDRe.MatchString(blob) {
		return nil, fmt.Errorf("%w: %q", ErrBadRev, blob)
	}
	out, err := g.Run(ctx, repo, nil, "cat-file", "-s", blob)
	if err != nil {
		return nil, err
	}
	if size, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); limit > 0 && size > limit {
		return nil, fmt.Errorf("%s (%d bytes): %w", blob, size, ErrTooLarge)
	}
	return g.Run(ctx, repo, nil, "cat-file", "blob", blob)
}

var blobIDRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ParseLFSPointer returns the object of an LFS pointer file, ok false if
// content is not one.
func ParseLFSPointer(content []byte) (oid string, size int64, ok bool) {
	if len(content) > 1024 || !bytes.HasPrefix(content, []byte("version https://git-lfs.github.com/spec/")) {
		return "", 0, false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if v, found := strings.CutPrefix(line, "oid sha256:"); found {
			oid = strings.TrimSpace(v)
		}
		if v, found := strings.CutPrefix(line, "size "); found {
			size, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return oid, size, ValidOID(oid)
}
