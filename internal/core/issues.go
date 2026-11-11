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
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ---- Local issues (D-028, T-9.13) and sending them (D-031, T-9.16) ----
//
// Local issues live next to the mirrored ones, in issues/local/<n>.md, in
// the same format (Markdown with YAML front matter, comments appended).
// They are named "L<n>"; mirrored issues keep their number ("#12").
//
// Own projects and forks with an original may change mirrored issues too.
// Every change meant for the original is kept in issues/outbox.yaml until
// it is sent: at once when the repository's switch is on, otherwise when
// the user asks. Mirrored issues of mirrors stay read-only.

// LocalIssuesDir is the folder of local issues below the issues folder.
const LocalIssuesDir = "local"

// outboxFile keeps changes not yet sent to the original.
const outboxFile = "outbox.yaml"

// Issue states.
const (
	IssueOpen   = "open"
	IssueClosed = "closed"
)

// Kinds of outgoing changes.
const (
	OpCreate  = "create"
	OpComment = "comment"
	OpState   = "state"
)

// sendTimeout bounds sending changes in a request.
const sendTimeout = 30 * time.Second

// issueLocks serialise changes to the issues and the outbox of one
// repository; sending to the host only blocks that repository (R-006).
var issueLocks sync.Map // repository ID → *sync.Mutex

func issueLock(id string) *sync.Mutex {
	m, _ := issueLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// IssueRef names an issue: local (L3) or mirrored (#12).
type IssueRef struct {
	Local  bool
	Number int
}

func (r IssueRef) String() string {
	if r.Local {
		return fmt.Sprintf("L%d", r.Number)
	}
	return fmt.Sprintf("#%d", r.Number)
}

// ParseIssueRef reads "L3" (local) or "#12" / "12" (mirrored).
func ParseIssueRef(ref string) (IssueRef, error) {
	s := strings.TrimSpace(ref)
	local := strings.HasPrefix(s, "L") || strings.HasPrefix(s, "l")
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "L"), "l"), "#")
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return IssueRef{}, fmt.Errorf("%w: issue %q (erwartet z. B. L3 oder #12)", ErrInvalid, ref)
	}
	return IssueRef{Local: local, Number: n}, nil
}

// IssueOp is a change waiting to be sent to the original.
type IssueOp struct {
	ID      string    `yaml:"id"`
	Kind    string    `yaml:"kind"`
	Issue   string    `yaml:"issue"` // "L3" or "#12"
	Text    string    `yaml:"text,omitempty"`
	State   string    `yaml:"state,omitempty"`
	Author  string    `yaml:"author"`
	Created time.Time `yaml:"created"`
	Error   string    `yaml:"error,omitempty"`
}

func (l *Library) issuesDir(r *Repo) string { return filepath.Join(r.Paths.Meta, IssuesDir) }
func (l *Library) localIssuesDir(r *Repo) string {
	return filepath.Join(l.issuesDir(r), LocalIssuesDir)
}

// canSendIssues reports whether a repository has an original that takes
// issue changes (own project or fork, host with a known API).
func (l *Library) canSendIssues(r *Repo) bool {
	rr := r.R()
	if rr.Kind == repometa.KindMirror || rr.Source.Host == repometa.LocalHost {
		return false
	}
	_, err := l.Sources.Writer(rr.Source.Host)
	return err == nil
}

// CanSendIssues is canSendIssues for the interfaces.
func (l *Library) CanSendIssues(r *Repo) bool { return l.canSendIssues(r) }

// CreateIssue adds a local issue and returns its number. For own projects
// with an original the new issue also waits to be sent.
func (l *Library) CreateIssue(ctx context.Context, ref, title, body string, labels []string, actor string) (int, error) {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		return 0, fmt.Errorf("%w: Titel fehlt", ErrInvalid)
	}
	r, err := l.Get(ctx, ref)
	if err != nil {
		return 0, err
	}
	issueLock(r.R().ID).Lock()
	dir := l.localIssuesDir(r)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		issueLock(r.R().ID).Unlock()
		return 0, err
	}
	n := 1
	for _, is := range readIssues(dir) {
		n = max(n, is.Number+1)
	}
	now := l.now().UTC().Truncate(time.Second)
	f := issueFront{Number: n, Title: title, State: IssueOpen, Author: actorName(actor), Labels: cleanLabels(labels),
		Created: now, Updated: now, Source: "local"}
	err = writeLocalIssue(dir, f, strings.TrimSpace(body))
	if err == nil && l.canSendIssues(r) {
		err = l.queue(r, IssueOp{Kind: OpCreate, Issue: IssueRef{Local: true, Number: n}.String(), Author: actor})
	}
	issueLock(r.R().ID).Unlock()
	if err != nil {
		return 0, err
	}
	l.Record(Activity{Actor: actor, Action: "issue-created", RepoID: r.R().ID, Detail: fmt.Sprintf("L%d %s", n, title)})
	l.afterIssueChange(ctx, r)
	return n, nil
}

// CommentIssue appends a comment to an issue.
func (l *Library) CommentIssue(ctx context.Context, ref string, is IssueRef, text, actor string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("%w: Kommentar ist leer", ErrInvalid)
	}
	return l.changeIssue(ctx, ref, is, actor, "issue-commented", IssueOp{Kind: OpComment, Text: text},
		func(f *issueFront, body *string) {
			*body = appendComment(*body, f, actorName(actor), l.now(), text)
		})
}

// SetIssueState closes or reopens an issue, optionally with a comment.
func (l *Library) SetIssueState(ctx context.Context, ref string, is IssueRef, state, comment, actor string) error {
	if state != IssueOpen && state != IssueClosed {
		return fmt.Errorf("%w: Zustand %q", ErrInvalid, state)
	}
	action := map[string]string{IssueOpen: "issue-reopened", IssueClosed: "issue-closed"}[state]
	if c := strings.TrimSpace(comment); c != "" {
		if err := l.CommentIssue(ctx, ref, is, c, actor); err != nil {
			return err
		}
	}
	return l.changeIssue(ctx, ref, is, actor, action, IssueOp{Kind: OpState, State: state},
		func(f *issueFront, _ *string) {
			f.State = state
			f.Closed = nil
			if state == IssueClosed {
				t := l.now().UTC().Truncate(time.Second)
				f.Closed = &t
			}
		})
}

// changeIssue changes a local issue file and queues the change for the
// original; a mirrored issue is only changed through the queue (its file
// is rewritten by the next sync).
func (l *Library) changeIssue(ctx context.Context, ref string, is IssueRef, actor, action string, op IssueOp, change func(*issueFront, *string)) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	send := l.canSendIssues(r)
	issueLock(r.R().ID).Lock()
	var title string
	if is.Local {
		dir := l.localIssuesDir(r)
		f, body, rerr := readIssueFile(filepath.Join(dir, fmt.Sprintf("%d.md", is.Number)))
		if errors.Is(rerr, os.ErrNotExist) {
			issueLock(r.R().ID).Unlock()
			return fmt.Errorf("%w: lokales Issue %s", ErrNotFound, is)
		}
		if rerr != nil {
			issueLock(r.R().ID).Unlock()
			return rerr
		}
		change(&f, &body)
		f.Updated = l.now().UTC().Truncate(time.Second)
		title = f.Title
		err = writeLocalIssue(dir, f, body)
		// Only issues that exist (or will exist) at the original are sent.
		send = send && (f.Remote > 0 || l.hasPendingCreate(r, is))
	} else {
		if !send {
			issueLock(r.R().ID).Unlock()
			return fmt.Errorf("%w: gespiegelte Issues sind schreibgeschützt (nur bei eigenen Projekten und Forks änderbar)", ErrReadOnly)
		}
		f, _, rerr := readIssueFile(filepath.Join(l.issuesDir(r), fmt.Sprintf("%d.md", is.Number)))
		if rerr != nil {
			issueLock(r.R().ID).Unlock()
			return fmt.Errorf("%w: Issue %s", ErrNotFound, is)
		}
		title = f.Title
	}
	if err == nil && send {
		op.Issue, op.Author = is.String(), actor
		err = l.queue(r, op)
	}
	issueLock(r.R().ID).Unlock()
	if err != nil {
		return err
	}
	l.Record(Activity{Actor: actor, Action: action, RepoID: r.R().ID, Detail: is.String() + " " + title})
	l.afterIssueChange(ctx, r)
	return nil
}

// afterIssueChange sends at once when the repository's switch is on and
// updates the search index.
func (l *Library) afterIssueChange(ctx context.Context, r *Repo) {
	if r.R().Sync.IssuesUpstream && l.canSendIssues(r) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
		defer cancel()
		_, _, _ = l.SendIssues(ctx, r.R().ID, "issues-upstream")
	}
	l.refreshEntry(ctx, r)
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
}

// refreshEntry updates the index entry (issues are searchable, T-9.20).
func (l *Library) refreshEntry(ctx context.Context, r *Repo) {
	r.Entry = l.entry(ctx, r, true)
	if err := l.Store.Upsert(ctx, r.Entry); err != nil {
		l.Log.Warn("index after issue change", "repo", r.R().ID, "err", err)
	}
}

// maxIssueText bounds the issue text per repository in the index.
const maxIssueText = 512 << 10

// issueText joins titles and texts of all issues for the search index.
func (l *Library) issueText(r *Repo) string {
	var b strings.Builder
	for _, dir := range []string{l.issuesDir(r), l.localIssuesDir(r)} {
		for _, is := range readIssues(dir) {
			if b.Len() > maxIssueText {
				return b.String()
			}
			b.WriteString(is.Title)
			b.WriteString("\n")
			b.WriteString(is.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// PendingIssues returns the changes waiting to be sent.
func (l *Library) PendingIssues(r *Repo) []IssueOp {
	issueLock(r.R().ID).Lock()
	defer issueLock(r.R().ID).Unlock()
	return l.readOutbox(r)
}

// SendIssues sends the waiting changes to the original, in order. A change
// that fails stays (with its error) and later ones of the same issue wait.
func (l *Library) SendIssues(ctx context.Context, ref, actor string) (sent, failed int, err error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return 0, 0, err
	}
	if !l.canSendIssues(r) {
		return 0, 0, fmt.Errorf("%w: dieses Repo hat kein Original, das Issues annimmt", ErrInvalid)
	}
	w, _ := l.Sources.Writer(r.R().Source.Host)
	src := r.R().Source
	issueLock(r.R().ID).Lock()
	ops := l.readOutbox(r)
	var keep []IssueOp
	blocked := map[string]bool{}
	for _, op := range ops {
		if blocked[op.Issue] {
			keep = append(keep, op)
			continue
		}
		if e := l.sendOp(ctx, r, w, src, op); e != nil {
			op.Error = e.Error()
			keep = append(keep, op)
			blocked[op.Issue] = true
			failed++
			continue
		}
		sent++
	}
	werr := l.writeOutbox(r, keep)
	issueLock(r.R().ID).Unlock()
	if werr != nil {
		return sent, failed, werr
	}
	if sent > 0 {
		l.Record(Activity{Actor: actor, Action: "issues-sent", RepoID: r.R().ID, Detail: fmt.Sprintf("%d gesendet, %d fehlgeschlagen", sent, failed)})
		// Fetch the issues back so the sent changes show as mirrored.
		unlock := l.locks.lock(r.R().ID)
		if l.syncIssues(ctx, r) == nil {
			if err := writeUpstream(r.Paths.Meta, r.Upstream); err != nil {
				l.Log.Warn("write upstream", "err", err)
			}
		}
		unlock()
		l.refreshEntry(ctx, r)
	}
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
	return sent, failed, nil
}

func (l *Library) sendOp(ctx context.Context, r *Repo, w sources.IssueWriter, src repometa.Source, op IssueOp) error {
	is, err := ParseIssueRef(op.Issue)
	if err != nil {
		return err
	}
	number := is.Number
	var local issueFront
	var localPath string
	if is.Local {
		localPath = filepath.Join(l.localIssuesDir(r), fmt.Sprintf("%d.md", is.Number))
		f, _, err := readIssueFile(localPath)
		if err != nil {
			return err
		}
		local, number = f, f.Remote
		if op.Kind != OpCreate && number == 0 {
			return errors.New("das Issue ist beim Original noch nicht angelegt")
		}
	}
	text := op.Text
	if a := actorName(op.Author); a != "ich" {
		text += "\n\n_— über RepoShelf, von " + a + "_"
	}
	switch op.Kind {
	case OpCreate:
		if local.Remote > 0 {
			return nil // already there
		}
		_, body, err := readIssueFile(localPath)
		if err != nil {
			return err
		}
		// Comments of a local issue stay local: only the description goes.
		desc, _, _ := strings.Cut(body, "\n\n---\n\n")
		if a := actorName(local.Author); a != "ich" {
			desc += "\n\n_— über RepoShelf, von " + a + "_"
		}
		n, err := w.CreateIssue(ctx, src, local.Title, desc, local.Labels)
		if err != nil {
			return err
		}
		f, b, err := readIssueFile(localPath)
		if err != nil {
			return err
		}
		f.Remote = n
		return writeLocalIssue(filepath.Dir(localPath), f, b)
	case OpComment:
		return w.CommentIssue(ctx, src, number, text)
	case OpState:
		return w.SetIssueState(ctx, src, number, op.State)
	}
	return fmt.Errorf("unknown change %q", op.Kind)
}

func (l *Library) hasPendingCreate(r *Repo, is IssueRef) bool {
	for _, op := range l.readOutbox(r) {
		if op.Kind == OpCreate && op.Issue == is.String() {
			return true
		}
	}
	return false
}

func (l *Library) queue(r *Repo, op IssueOp) error {
	op.ID = repometa.NewID(l.now())
	op.Created = l.now().UTC().Truncate(time.Second)
	if err := os.MkdirAll(l.issuesDir(r), dirPerm); err != nil {
		return err
	}
	return l.writeOutbox(r, append(l.readOutbox(r), op))
}

func (l *Library) readOutbox(r *Repo) []IssueOp {
	b, err := os.ReadFile(filepath.Join(l.issuesDir(r), outboxFile))
	if err != nil {
		return nil
	}
	var ops []IssueOp
	if yaml.Unmarshal(b, &ops) != nil {
		l.Log.Warn("issue outbox unreadable", "repo", r.R().ID)
		return nil
	}
	return ops
}

func (l *Library) writeOutbox(r *Repo, ops []IssueOp) error {
	p := filepath.Join(l.issuesDir(r), outboxFile)
	if len(ops) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := yaml.Marshal(ops)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(p, b, filePerm)
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
			Labels: f.Labels, Body: body, Local: f.Source == "local", URL: f.URL, Comments: f.Comments,
			Updated: f.Updated, Remote: f.Remote})
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

// actorName is the author shown in an issue ("web", "cli" → "ich").
func actorName(actor string) string {
	switch actor {
	case "", "web", "cli", "issues-upstream":
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
