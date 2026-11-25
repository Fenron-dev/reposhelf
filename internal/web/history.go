// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
)

// Commits, one commit and comparing versions (V-6, T-9.31, D-036).

const (
	commitsPerPage = 50
	// maxCompareCommits listed when comparing; the diff is complete anyway.
	maxCompareCommits = 250
	// maxDiffBytes of patch shown; beyond, files only show their counts.
	maxDiffBytes = 2 << 20
	// maxFileLines shown per file before it is collapsed.
	maxFileLines = 1500
)

// historyView is the "Commits" tab: a commit list, one commit or a comparison.
type historyView struct {
	Mode        string // list, commit, compare
	Ref         string
	Page        int
	More        bool
	Commits     []gitcli.Commit
	Commit      *gitcli.CommitDetail
	From, To    string
	Diff        *gitcli.Diff
	MaxCommits  int
	MaxFileRows int
}

func (s *Server) repoCommits(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	d := s.baseRepoData(r, repo)
	q := r.URL.Query()
	h := &historyView{Mode: "list", Ref: q.Get("ref"), Page: 1}
	if h.Ref == "" {
		h.Ref = "HEAD"
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 1 {
		h.Page = p
	}
	list, err := s.Lib.Git.LogPage(r.Context(), repo.Paths.Mirror, h.Ref, (h.Page-1)*commitsPerPage, commitsPerPage+1)
	if err != nil {
		s.failErr(w, r, revErr(h.Ref, err))
		return
	}
	h.More = len(list) > commitsPerPage
	h.Commits = list[:min(len(list), commitsPerPage)]
	s.showHistory(w, r, &d, h)
}

func (s *Server) repoCommit(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	d := s.baseRepoData(r, repo)
	rev := r.PathValue("rev")
	c, err := s.Lib.Git.CommitDetail(r.Context(), repo.Paths.Mirror, rev)
	if err != nil {
		s.failErr(w, r, revErr(rev, err))
		return
	}
	from := ""
	if len(c.Parents) > 0 {
		from = c.Parents[0] // merges: what the merge brought into the first parent
	}
	diff, err := s.Lib.Git.Diff(r.Context(), repo.Paths.Mirror, from, c.Hash, maxDiffBytes)
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	s.showHistory(w, r, &d, &historyView{Mode: "commit", Commit: &c, Diff: diff})
}

func (s *Server) repoCompare(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	d := s.baseRepoData(r, repo)
	q := r.URL.Query()
	h := &historyView{Mode: "compare", From: q.Get("from"), To: q.Get("to")}
	if h.To == "" {
		h.To = "HEAD"
	}
	if h.From != "" {
		ctx, mirror := r.Context(), repo.Paths.Mirror
		list, err := s.Lib.Git.LogRange(ctx, mirror, h.From, h.To, maxCompareCommits+1)
		if err != nil {
			s.failErr(w, r, revErr(h.From+".."+h.To, err))
			return
		}
		h.More = len(list) > maxCompareCommits
		h.Commits = list[:min(len(list), maxCompareCommits)]
		if h.Diff, err = s.Lib.Git.Diff(ctx, mirror, h.From, h.To, maxDiffBytes); err != nil {
			s.failErr(w, r, revErr(h.From+".."+h.To, err))
			return
		}
	}
	s.showHistory(w, r, &d, h)
}

func (s *Server) showHistory(w http.ResponseWriter, r *http.Request, d *repoData, h *historyView) {
	ctx, mirror := r.Context(), d.Repo.Paths.Mirror
	d.Tab, d.History = "commits", h
	h.MaxCommits, h.MaxFileRows = maxCompareCommits, maxFileLines
	d.Branches, _ = s.Lib.Git.Branches(ctx, mirror)
	d.Versions, _ = s.Lib.Git.Versions(ctx, mirror)
	s.render(w, r, "repo", d.Repo.Entry.Owner+" / "+d.Repo.Entry.Name, "library", *d)
}

// revErr turns an unknown or invalid revision into "not found".
func revErr(rev string, err error) error {
	if errors.Is(err, gitcli.ErrBadRev) {
		return fmt.Errorf("%w: ungültige Angabe %q", core.ErrInvalid, rev)
	}
	return fmt.Errorf("%q: %w", rev, core.ErrNotFound)
}

// diffClass is the CSS class of a diff line.
func diffClass(k byte) string {
	switch k {
	case '+':
		return "add"
	case '-':
		return "del"
	case '\\':
		return "note"
	}
	return "ctx"
}
