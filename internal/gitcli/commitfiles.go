// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git's object IDs, not a security hash
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Files are put into a bare repository without a working copy (T-9.17):
// large files become Git LFS objects (pointer in the tree, content below
// lfs/objects), small ones ordinary blobs. Nothing is copied twice.

// LFSObjectPath is where git-lfs keeps an object of a bare repository.
func LFSObjectPath(bare, oid string) string {
	return filepath.Join(bare, "lfs", "objects", oid[:2], oid[2:4], oid)
}

var oidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LFSPointer is the text git stores instead of a large file.
func LFSPointer(oid string, size int64) string {
	return fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, size)
}

// FileEntry is one file of a commit: a blob already in the repository.
type FileEntry struct {
	Path string // slash separated, relative
	Blob string // git object ID
	Exec bool
}

// HashObject writes content as a blob and returns its ID.
func (g *Git) HashObject(ctx context.Context, dir string, content io.Reader) (string, error) {
	out, err := g.runInput(ctx, dir, content, nil, "hash-object", "-w", "--stdin")
	return strings.TrimSpace(string(out)), err
}

// BlobID is the ID git gives content of this size (without writing it).
func BlobID(r io.Reader, size int64) (string, error) {
	h := sha1.New() //nolint:gosec // git object ID
	fmt.Fprintf(h, "blob %d\x00", size)
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HasObjects reports which of the object IDs the repository has.
func (g *Git) HasObjects(ctx context.Context, dir string, ids []string) (map[string]bool, error) {
	out, err := g.runInput(ctx, dir, strings.NewReader(strings.Join(ids, "\n")+"\n"), nil, "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[1] == "blob" {
			have[f[0]] = true
		}
	}
	return have, nil
}

// CommitFiles commits the files on branch of the bare repository dir
// (parent: the branch tip, if any) and returns the commit ID.
func (g *Git) CommitFiles(ctx context.Context, dir, branch, message string, files []FileEntry) (string, error) {
	if len(files) == 0 {
		return "", errors.New("no files to commit")
	}
	if err := checkRev(branch); err != nil {
		return "", err
	}
	index, err := os.CreateTemp("", "reposhelf-index-*")
	if err != nil {
		return "", err
	}
	index.Close()
	os.Remove(index.Name()) // git creates it
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name(), "GIT_AUTHOR_NAME=RepoShelf", "GIT_AUTHOR_EMAIL=reposhelf@localhost",
		"GIT_COMMITTER_NAME=RepoShelf", "GIT_COMMITTER_EMAIL=reposhelf@localhost"}
	var info bytes.Buffer
	for _, f := range files {
		p := path.Clean(f.Path)
		if p == "." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n") {
			return "", fmt.Errorf("unsafe path %q", f.Path)
		}
		mode := "100644"
		if f.Exec {
			mode = "100755"
		}
		fmt.Fprintf(&info, "%s %s\t%s\n", mode, f.Blob, p)
	}
	if _, err := g.runInput(ctx, dir, &info, env, "update-index", "--add", "--index-info"); err != nil {
		return "", err
	}
	tree, err := g.runInput(ctx, dir, nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", message}
	if parent := strings.TrimSpace(g.revParse(ctx, dir, "refs/heads/"+branch)); parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := g.runInput(ctx, dir, nil, env, args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(commit))
	if _, err := g.Run(ctx, dir, nil, "update-ref", "refs/heads/"+branch, id); err != nil {
		return "", err
	}
	return id, nil
}

func (g *Git) revParse(ctx context.Context, dir, rev string) string {
	out, err := g.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return ""
	}
	return string(out)
}

// StoreLFS puts the file src into the LFS store of the bare repository and
// returns its object ID and size. link hard-links instead of copying when
// possible (same volume). The content is hashed while it is copied; an
// object that already exists is kept.
func StoreLFS(bare, src string, link bool) (string, int64, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	if link {
		if _, err := io.Copy(h, f); err != nil {
			return "", 0, err
		}
		oid := hex.EncodeToString(h.Sum(nil))
		dst := LFSObjectPath(bare, oid)
		if _, err := os.Stat(dst); err == nil {
			return oid, info.Size(), nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", 0, err
		}
		if os.Link(src, dst) == nil {
			return oid, info.Size(), nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", 0, err
		}
		h.Reset()
	}
	tmp, err := os.CreateTemp(filepath.Join(bare, "lfs"), "incoming-*")
	if err != nil {
		if err := os.MkdirAll(filepath.Join(bare, "lfs"), 0o755); err != nil {
			return "", 0, err
		}
		if tmp, err = os.CreateTemp(filepath.Join(bare, "lfs"), "incoming-*"); err != nil {
			return "", 0, err
		}
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(io.MultiWriter(tmp, h), f); err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	oid := hex.EncodeToString(h.Sum(nil))
	dst := LFSObjectPath(bare, oid)
	if _, err := os.Stat(dst); err == nil {
		return oid, info.Size(), nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	return oid, info.Size(), os.Rename(tmp.Name(), dst)
}

// HasLFS reports whether the LFS store holds the object (content checked:
// its SHA-256 must match the name).
func HasLFS(bare, oid string) bool {
	if !oidRe.MatchString(oid) {
		return false
	}
	f, err := os.Open(LFSObjectPath(bare, oid))
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == oid
}

// LFSAttributes returns .gitattributes lines for the LFS paths: one per
// extension, exact paths for files without one.
func LFSAttributes(paths []string) string {
	var lines []string
	for _, p := range paths {
		ext := path.Ext(p)
		line := ""
		if ext != "" && !strings.ContainsAny(ext, " \t*?[]") {
			line = "*" + ext
		} else {
			line = "/" + strings.NewReplacer(" ", "[[:space:]]", "\t", "[[:space:]]").Replace(p)
		}
		line += " filter=lfs diff=lfs merge=lfs -text"
		if !slices.Contains(lines, line) {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n") + "\n"
}

// runInput runs git with standard input and extra environment.
func (g *Git) runInput(ctx context.Context, dir string, stdin io.Reader, env []string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := g.command(ctx, dir, nil, args...)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = stdin
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), classify(&Error{Args: args, Stderr: stderr.String(), Err: err}, ctx)
	}
	return stdout.Bytes(), nil
}

// SHA256 returns the hex SHA-256 of r (the name of an LFS object).
func SHA256(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
