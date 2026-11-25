// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ---- History and diffs (V-6, T-9.31) ----
//
// Commit lists, one commit and the difference between two revisions, read
// from the bare mirror. Diffs of foreign repositories can be huge (vendored
// code, generated files), so the patch is streamed and cut at a byte limit;
// the list of changed files comes from --numstat and is always complete.

// CommitDetail is a commit with its message and parents.
type CommitDetail struct {
	Commit
	Committer  string
	CommitTime time.Time
	Body       string
	Parents    []string
}

// LogPage returns up to n commits of rev, skipping the newest skip.
func (g *Git) LogPage(ctx context.Context, repo, rev string, skip, n int) ([]Commit, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	return g.log(ctx, repo, rev, skip, n)
}

// LogRange returns up to n commits in to that are not in from, newest first.
func (g *Git) LogRange(ctx context.Context, repo, from, to string, n int) ([]Commit, error) {
	if err := checkRev(from); err != nil {
		return nil, err
	}
	if err := checkRev(to); err != nil {
		return nil, err
	}
	return g.log(ctx, repo, from+".."+to, 0, n)
}

func (g *Git) log(ctx context.Context, repo, rev string, skip, n int) ([]Commit, error) {
	out, err := g.Run(ctx, repo, nil, "log", "-n", strconv.Itoa(n), "--skip", strconv.Itoa(skip),
		"--format=%H%x00%an%x00%aI%x00%s", "--end-of-options", rev, "--")
	if err != nil {
		return nil, err
	}
	var list []Commit
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
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

// CommitDetail reads one commit.
func (g *Git) CommitDetail(ctx context.Context, repo, rev string) (CommitDetail, error) {
	if err := checkRev(rev); err != nil {
		return CommitDetail{}, err
	}
	out, err := g.Run(ctx, repo, nil, "show", "-s", "--format=%H%x00%an%x00%aI%x00%cn%x00%cI%x00%P%x00%s%x00%b", "--end-of-options", rev+"^{commit}", "--")
	if err != nil {
		return CommitDetail{}, fmt.Errorf("commit %s: %w", rev, err)
	}
	f := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 8)
	if len(f) != 8 {
		return CommitDetail{}, fmt.Errorf("commit %s: unexpected output", rev)
	}
	d := CommitDetail{Commit: Commit{Hash: f[0], Author: f[1], Subject: f[6]}, Committer: f[3], Body: strings.TrimSpace(f[7]), Parents: strings.Fields(f[5])}
	d.Time, _ = time.Parse(time.RFC3339, f[2])
	d.CommitTime, _ = time.Parse(time.RFC3339, f[4])
	return d, nil
}

// Diff is the difference between two revisions.
type Diff struct {
	Files []FileDiff
	// Truncated: the patch was cut at the byte limit; files after the cut
	// have counts but no lines.
	Truncated  bool
	Adds, Dels int
}

// FileDiff is one changed file.
type FileDiff struct {
	OldPath, NewPath string
	Status           string // added, deleted, renamed, modified
	Binary           bool
	Adds, Dels       int
	Hunks            []Hunk
	// Shown: the patch of this file was read (not cut off).
	Shown bool
}

// Path is the name to show: the new path, or the old one for deletions.
func (f FileDiff) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// Hunk is one block of changes.
type Hunk struct {
	Header string
	Lines  []DiffLine
}

// DiffLine is one line of a hunk; Kind is '+', '-', ' ' or '\\' (no newline).
type DiffLine struct {
	Kind     byte
	Old, New int // line numbers, 0 where not applicable
	Text     string
}

// Diff compares two revisions (git diff from to). With from == "" it shows
// what the root commit to introduced. maxBytes bounds the patch read.
func (g *Git) Diff(ctx context.Context, repo, from, to string, maxBytes int64) (*Diff, error) {
	if err := checkRev(to); err != nil {
		return nil, err
	}
	common := []string{"-c", "core.quotepath=off"}
	var stat, patch []string
	if from == "" {
		base := []string{"diff-tree", "--root", "--no-commit-id", "-r", "-M", "--no-color", "--no-ext-diff", "--no-textconv"}
		stat = append(append(append(common, base...), "--numstat", "-z", "--end-of-options", to), "--")
		patch = append(append(append(common, base...), "-p", "--src-prefix=a/", "--dst-prefix=b/", "--end-of-options", to), "--")
	} else {
		if err := checkRev(from); err != nil {
			return nil, err
		}
		base := []string{"diff", "-M", "--no-color", "--no-ext-diff", "--no-textconv"}
		stat = append(append(append(common, base...), "--numstat", "-z", "--end-of-options", from, to), "--")
		patch = append(append(append(common, base...), "--src-prefix=a/", "--dst-prefix=b/", "--end-of-options", from, to), "--")
	}
	out, err := g.Run(ctx, repo, nil, stat...)
	if err != nil {
		return nil, fmt.Errorf("diff %s %s: %w", from, to, err)
	}
	d := &Diff{Files: parseNumstat(out)}
	for _, f := range d.Files {
		d.Adds += f.Adds
		d.Dels += f.Dels
	}
	raw, truncated, err := g.runLimited(ctx, repo, maxBytes, patch...)
	if err != nil {
		return nil, fmt.Errorf("diff %s %s: %w", from, to, err)
	}
	d.Truncated = truncated
	parsed := parsePatch(raw, truncated)
	byPath := map[string]*FileDiff{}
	for i := range d.Files {
		byPath[d.Files[i].Path()] = &d.Files[i]
	}
	for _, p := range parsed {
		f := byPath[p.Path()]
		if f == nil {
			continue
		}
		f.Status, f.Hunks, f.Shown = p.Status, p.Hunks, p.Shown
		if p.Binary {
			f.Binary = true
		}
	}
	return d, nil
}

// runLimited runs git and reads at most limit bytes of its output; then the
// process is stopped and truncated is true.
func (g *Git) runLimited(ctx context.Context, dir string, limit int64, args ...string) (out []byte, truncated bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := g.command(ctx, dir, nil, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	out, readErr := io.ReadAll(io.LimitReader(pipe, limit+1))
	if int64(len(out)) > limit {
		out, truncated = out[:limit], true
		cancel()
	}
	waitErr := cmd.Wait()
	switch {
	case truncated:
		return out, true, nil
	case readErr != nil:
		return nil, false, readErr
	case waitErr != nil:
		return nil, false, classify(&Error{Args: args, Stderr: stderr.String(), Err: waitErr}, ctx)
	}
	return out, false, nil
}

// parseNumstat reads "adds\tdels\tpath\0" or, for renames,
// "adds\tdels\t\0old\0new\0". Binary files have "-" counts.
func parseNumstat(b []byte) []FileDiff {
	var out []FileDiff
	parts := strings.Split(string(b), "\x00")
	for i := 0; i < len(parts); i++ {
		head := strings.TrimLeft(parts[i], "\n")
		f := strings.SplitN(head, "\t", 3)
		if len(f) != 3 {
			continue
		}
		fd := FileDiff{Status: "modified"}
		if f[0] == "-" {
			fd.Binary = true
		}
		fd.Adds, _ = strconv.Atoi(f[0])
		fd.Dels, _ = strconv.Atoi(f[1])
		if f[2] == "" && i+2 < len(parts) {
			fd.OldPath, fd.NewPath, fd.Status = parts[i+1], parts[i+2], "renamed"
			i += 2
		} else {
			fd.OldPath, fd.NewPath = f[2], f[2]
		}
		out = append(out, fd)
	}
	return out
}

// parsePatch reads a unified diff as git writes it. The last file of a
// truncated patch is marked as not shown.
func parsePatch(b []byte, truncated bool) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	var oldN, newN int
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
		}
		cur, hunk = nil, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), maxPatchLine)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			oldP, newP := splitDiffHeader(strings.TrimPrefix(line, "diff --git "))
			cur = &FileDiff{OldPath: oldP, NewPath: newP, Status: "modified", Shown: true}
		case cur == nil:
		case hunk == nil && strings.HasPrefix(line, "new file mode"):
			cur.Status, cur.OldPath = "added", ""
		case hunk == nil && strings.HasPrefix(line, "deleted file mode"):
			cur.Status, cur.NewPath = "deleted", ""
		case hunk == nil && strings.HasPrefix(line, "rename from "):
			cur.Status, cur.OldPath = "renamed", unquotePath(strings.TrimPrefix(line, "rename from "))
		case hunk == nil && strings.HasPrefix(line, "rename to "):
			cur.NewPath = unquotePath(strings.TrimPrefix(line, "rename to "))
		case hunk == nil && strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case hunk == nil && strings.HasPrefix(line, "--- "):
			// Names with spaces end in a tab in these lines.
			if p := strings.TrimSuffix(strings.TrimPrefix(line, "--- "), "\t"); p != "/dev/null" {
				cur.OldPath = strings.TrimPrefix(unquotePath(p), "a/")
			}
		case hunk == nil && strings.HasPrefix(line, "+++ "):
			if p := strings.TrimSuffix(strings.TrimPrefix(line, "+++ "), "\t"); p != "/dev/null" {
				cur.NewPath = strings.TrimPrefix(unquotePath(p), "b/")
			}
		case strings.HasPrefix(line, "@@ "):
			cur.Hunks = append(cur.Hunks, Hunk{Header: line})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
			oldN, newN = hunkStart(line)
		case hunk != nil && line != "":
			dl := DiffLine{Kind: line[0], Text: line[1:]}
			switch dl.Kind {
			case '+':
				dl.New, newN = newN, newN+1
			case '-':
				dl.Old, oldN = oldN, oldN+1
			case ' ':
				dl.Old, dl.New, oldN, newN = oldN, newN, oldN+1, newN+1
			case '\\':
			default:
				continue
			}
			hunk.Lines = append(hunk.Lines, dl)
		}
	}
	if truncated && cur != nil {
		cur.Shown = false
	}
	flush()
	return files
}

// maxPatchLine: longer lines (minified code) end the parse of the patch.
const maxPatchLine = 1 << 20

// hunkStart reads the first old and new line of "@@ -a,b +c,d @@".
func hunkStart(h string) (old, nw int) {
	f := strings.Fields(h)
	if len(f) < 3 {
		return 0, 0
	}
	num := func(s string) int {
		s = strings.TrimLeft(s, "-+")
		n, _ := strconv.Atoi(strings.SplitN(s, ",", 2)[0])
		return n
	}
	return num(f[1]), num(f[2])
}

// splitDiffHeader splits "a/x b/x" (same path both sides); renames and
// quoted names are filled in by later header lines.
func splitDiffHeader(rest string) (string, string) {
	if strings.HasPrefix(rest, `"`) {
		return "", ""
	}
	if n := len(rest); n%2 == 1 {
		half := (n - 1) / 2
		a, b := rest[:half], rest[half+1:]
		if strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	return "", ""
}

// unquotePath undoes git's C-style quoting of unusual file names.
func unquotePath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}
