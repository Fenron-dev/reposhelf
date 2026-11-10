// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
)

// ---- Local issues (D-028, T-9.13) ----
//
// Local issues live next to the mirrored ones, in issues/local/<n>.md, in
// the same format (Markdown with YAML front matter, comments appended).
// They are referred to as "L<n>"; mirrored issues keep their number and
// stay read-only.

// LocalIssuesDir is the folder of local issues below the issues folder.
const LocalIssuesDir = "local"

// Issue states.
const (
	IssueOpen   = "open"
	IssueClosed = "closed"
)

// issueMu serialises changes to local issues (numbering, appending).
var issueMu sync.Mutex

func (l *Library) localIssuesDir(r *Repo) string {
	return filepath.Join(r.Paths.Meta, IssuesDir, LocalIssuesDir)
}

// ParseIssueRef turns "L3", "l3" or "3" into the number of a local issue.
func ParseIssueRef(ref string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(ref), "L"), "l"))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%w: issue %q (erwartet z. B. L3)", ErrInvalid, ref)
	}
	return n, nil
}

// CreateIssue adds a local issue and returns its number.
func (l *Library) CreateIssue(ctx context.Context, ref, title, body string, labels []string, actor string) (int, error) {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		return 0, fmt.Errorf("%w: Titel fehlt", ErrInvalid)
	}
	r, err := l.Get(ctx, ref)
	if err != nil {
		return 0, err
	}
	issueMu.Lock()
	defer issueMu.Unlock()
	dir := l.localIssuesDir(r)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return 0, err
	}
	n := 1
	for _, is := range readIssues(dir) {
		n = max(n, is.Number+1)
	}
	now := l.now().UTC().Truncate(time.Second)
	f := issueFront{Number: n, Title: title, State: IssueOpen, Author: actorName(actor), Labels: cleanLabels(labels),
		Created: now, Updated: now, Source: "local"}
	if err := writeLocalIssue(dir, f, strings.TrimSpace(body)); err != nil {
		return 0, err
	}
	l.Record(Activity{Actor: actor, Action: "issue-created", RepoID: r.R().ID, Detail: fmt.Sprintf("L%d %s", n, title)})
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
	return n, nil
}

// CommentIssue appends a comment to a local issue.
func (l *Library) CommentIssue(ctx context.Context, ref string, number int, text, actor string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("%w: Kommentar ist leer", ErrInvalid)
	}
	return l.changeIssue(ctx, ref, number, actor, "issue-commented", func(f *issueFront, body *string) {
		*body = appendComment(*body, f, actorName(actor), l.now(), text)
	})
}

// SetIssueState closes or reopens a local issue, optionally with a comment.
func (l *Library) SetIssueState(ctx context.Context, ref string, number int, state, comment, actor string) error {
	if state != IssueOpen && state != IssueClosed {
		return fmt.Errorf("%w: Zustand %q", ErrInvalid, state)
	}
	action := map[string]string{IssueOpen: "issue-reopened", IssueClosed: "issue-closed"}[state]
	return l.changeIssue(ctx, ref, number, actor, action, func(f *issueFront, body *string) {
		if c := strings.TrimSpace(comment); c != "" {
			*body = appendComment(*body, f, actorName(actor), l.now(), c)
		}
		f.State = state
		f.Closed = nil
		if state == IssueClosed {
			t := l.now().UTC().Truncate(time.Second)
			f.Closed = &t
		}
	})
}

func (l *Library) changeIssue(ctx context.Context, ref string, number int, actor, action string, change func(*issueFront, *string)) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	issueMu.Lock()
	defer issueMu.Unlock()
	dir := l.localIssuesDir(r)
	f, body, err := readIssueFile(filepath.Join(dir, fmt.Sprintf("%d.md", number)))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: lokales Issue L%d", ErrNotFound, number)
	}
	if err != nil {
		return err
	}
	change(&f, &body)
	f.Updated = l.now().UTC().Truncate(time.Second)
	if err := writeLocalIssue(dir, f, body); err != nil {
		return err
	}
	l.Record(Activity{Actor: actor, Action: action, RepoID: r.R().ID, Detail: fmt.Sprintf("L%d %s", number, f.Title)})
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
	return nil
}

func appendComment(body string, f *issueFront, author string, now time.Time, text string) string {
	f.Comments++
	return strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n---\n\n**%s** am %s:\n\n%s", author, now.Local().Format("2006-01-02 15:04"), text)
}

func writeLocalIssue(dir string, f issueFront, body string) error {
	front, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	content := "---\n" + string(front) + "---\n\n" + strings.TrimSpace(body) + "\n"
	return atomicfile.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.md", f.Number)), []byte(content), filePerm)
}

func readIssueFile(path string) (issueFront, string, error) {
	var f issueFront
	b, err := os.ReadFile(path)
	if err != nil {
		return f, "", err
	}
	if !strings.HasPrefix(string(b), "---\n") {
		return f, "", fmt.Errorf("%s: kein YAML-Kopf", path)
	}
	fm, body, ok := strings.Cut(string(b)[4:], "\n---\n")
	if !ok {
		return f, "", fmt.Errorf("%s: YAML-Kopf nicht abgeschlossen", path)
	}
	if err := yaml.Unmarshal([]byte(fm), &f); err != nil {
		return f, "", fmt.Errorf("%s: %w", path, err)
	}
	return f, strings.TrimSpace(body), nil
}

// readIssues reads all issue files of a folder (not recursive).
func readIssues(dir string) []IssueSummary {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []IssueSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		f, body, err := readIssueFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, IssueSummary{Number: f.Number, Title: f.Title, State: f.State, Author: f.Author,
			Labels: f.Labels, Body: body, Local: f.Source == "local", URL: f.URL, Comments: f.Comments, Updated: f.Updated})
	}
	return out
}

func sortIssues(out []IssueSummary) {
	slices.SortFunc(out, func(a, b IssueSummary) int {
		if a.State != b.State {
			if a.State == IssueOpen {
				return -1
			}
			return 1
		}
		if a.Local != b.Local {
			if a.Local {
				return -1
			}
			return 1
		}
		return b.Number - a.Number
	})
}

// actorName is the author shown in an issue ("web" → "Ich").
func actorName(actor string) string {
	switch actor {
	case "", "web", "cli":
		return "ich"
	}
	return actor
}

func cleanLabels(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
