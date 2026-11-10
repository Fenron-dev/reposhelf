// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- Issues (KONZEPT 6.5, T-9.1): Markdown files with YAML front matter ----

// IssuesDir is the folder of mirrored issues next to repo.yaml.
const IssuesDir = "issues"

func (l *Library) syncIssues(ctx context.Context, r *Repo) error {
	api, err := l.Sources.API(r.R().Source.Host)
	if err != nil {
		return err
	}
	issues, err := api.Issues(ctx, r.R().Source, r.Upstream.IssuesSynced)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.Paths.Meta, IssuesDir)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	for _, is := range issues {
		if err := writeIssue(dir, is); err != nil {
			return err
		}
	}
	now := l.now().UTC()
	r.Upstream.IssuesSynced = &now
	return nil
}

type issueFront struct {
	Number   int        `yaml:"number"`
	Title    string     `yaml:"title"`
	State    string     `yaml:"state"`
	Author   string     `yaml:"author"`
	Labels   []string   `yaml:"labels,flow"`
	Created  time.Time  `yaml:"created"`
	Updated  time.Time  `yaml:"updated"`
	Closed   *time.Time `yaml:"closed,omitempty"`
	URL      string     `yaml:"url,omitempty"`
	Comments int        `yaml:"comments"`
	// Source is "local" for issues created in RepoShelf (D-028).
	Source string `yaml:"source,omitempty"`
}

func writeIssue(dir string, is sources.Issue) error {
	front, err := yaml.Marshal(issueFront{Number: is.Number, Title: is.Title, State: is.State, Author: is.Author,
		Labels: is.Labels, Created: is.CreatedAt, Updated: is.UpdatedAt, Closed: is.ClosedAt, URL: is.URL, Comments: len(is.Comments)})
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(front)
	b.WriteString("---\n\n")
	b.WriteString(is.Body)
	for _, c := range is.Comments {
		fmt.Fprintf(&b, "\n\n---\n\n**%s** am %s:\n\n%s", c.Author, c.CreatedAt.Format("2006-01-02 15:04"), c.Body)
	}
	b.WriteString("\n")
	return atomicfile.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.md", is.Number)), []byte(b.String()), filePerm)
}

// IssueSummary is one issue for lists, mirrored or local.
type IssueSummary struct {
	Number   int
	Title    string
	State    string
	Author   string
	Labels   []string
	Body     string
	Local    bool // created in RepoShelf; referred to as L<Number>
	URL      string
	Comments int
	Updated  time.Time
}

// Ref is how the issue is named: "L3" for local, "#3" for mirrored ones.
func (i IssueSummary) Ref() string {
	if i.Local {
		return fmt.Sprintf("L%d", i.Number)
	}
	return fmt.Sprintf("#%d", i.Number)
}

// Issues lists the local and mirrored issues of a repository, open ones
// first.
func (l *Library) Issues(ctx context.Context, ref string) ([]IssueSummary, error) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	out := readIssues(filepath.Join(r.Paths.Meta, IssuesDir))
	for i := range out {
		out[i].Local = false // only files below local/ are local
	}
	out = append(out, readIssues(l.localIssuesDir(r))...)
	sortIssues(out)
	return out, nil
}

// ---- Wiki ----

func (l *Library) wikiDir(r *Repo) string {
	return strings.TrimSuffix(r.Paths.Mirror, layout.MirrorSuffix) + ".wiki" + layout.MirrorSuffix
}

func (l *Library) syncWiki(ctx context.Context, r *Repo, auth *gitcli.Auth) error {
	src := r.R().Source.URL
	if src == "" {
		return nil
	}
	url := strings.TrimSuffix(src, ".git") + ".wiki.git"
	dir := l.wikiDir(r)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := l.Git.InitBare(ctx, dir, url); err != nil {
			return err
		}
	}
	_, err := l.Git.Mirror(ctx, dir, url, auth, l.now())
	if errors.Is(err, gitcli.ErrGone) {
		return nil // no wiki upstream
	}
	return err
}

// ---- Submodules: mirror them as repositories of their own ----

var gitmodulesURL = regexp.MustCompile(`(?m)^\s*url\s*=\s*(\S+)\s*$`)

// maxAutoAdd bounds how many submodules one sync may add.
const maxAutoAdd = 20

func (l *Library) syncSubmodules(ctx context.Context, r *Repo) ([]string, error) {
	b, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", ".gitmodules", 1<<20)
	if err != nil {
		return nil, nil
	}
	var added []string
	for _, m := range gitmodulesURL.FindAllStringSubmatch(string(b), -1) {
		if len(added) >= maxAutoAdd {
			break
		}
		raw := m[1]
		if strings.HasPrefix(raw, "../") || strings.HasPrefix(raw, "./") {
			raw = resolveRelative(r.R().Source.URL, raw)
		}
		p, err := sources.ParseURL(raw)
		if err != nil {
			continue
		}
		target := p.Source.Key()
		addSuggestion(r.R(), "depends_on", target, "submodule")
		confirmRelation(r.R(), "depends_on", target)
		if _, err := l.Store.ByFolder(ctx, target); err == nil {
			continue
		}
		mode := r.R().Sync.Mode
		req := AddRequest{URL: raw, Kind: repometa.KindMirror, Mode: mode, Interval: r.R().Sync.Interval, Actor: "submodule", Tags: r.R().Tags}
		inc := repometa.Defaults(repometa.KindMirror).Include
		inc.Releases = false
		req.Include = &inc
		if _, err := l.Add(ctx, req); err == nil {
			added = append(added, target)
		} else if !errors.Is(err, ErrExists) {
			l.Log.Warn("submodule add", "url", raw, "err", err)
		}
	}
	return added, nil
}

func resolveRelative(base, rel string) string {
	b := strings.TrimSuffix(base, ".git")
	for strings.HasPrefix(rel, "../") {
		rel = rel[3:]
		if i := strings.LastIndex(b, "/"); i > 0 {
			b = b[:i]
		}
	}
	return b + "/" + strings.TrimPrefix(rel, "./")
}

// ---- Snapshots: periodic copy of all refs (KONZEPT 3) ----

// SnapshotNamespace holds periodic ref snapshots.
const SnapshotNamespace = "refs/reposhelf/snapshot/"

func (l *Library) snapshot(ctx context.Context, r *Repo) error {
	every := time.Duration(l.EffectiveRetention(r).SnapshotsEvery)
	if every <= 0 {
		return nil
	}
	now := l.now().UTC()
	if last := r.Upstream.LastSnapshot; last != nil && now.Sub(*last) < every {
		return nil
	}
	refs, err := l.Git.LocalRefs(ctx, r.Paths.Mirror)
	if err != nil || len(refs) == 0 {
		return err
	}
	prefix := SnapshotNamespace + now.Format("20060102T150405Z") + "/"
	for name, oid := range refs {
		if _, err := l.Git.Run(ctx, r.Paths.Mirror, nil, "update-ref", prefix+name, oid); err != nil {
			return err
		}
	}
	r.Upstream.LastSnapshot = &now
	return nil
}

// ---- License detection from the LICENSE file (KONZEPT 6.3) ----

var licenseHints = []struct{ id, needle string }{
	{"AGPL-3.0", "GNU AFFERO GENERAL PUBLIC LICENSE"},
	{"LGPL-3.0", "GNU LESSER GENERAL PUBLIC LICENSE"},
	{"GPL-3.0", "GNU GENERAL PUBLIC LICENSE\n                       Version 3"},
	{"GPL-3.0", "GNU GENERAL PUBLIC LICENSE Version 3"},
	{"GPL-2.0", "GNU GENERAL PUBLIC LICENSE\n                       Version 2"},
	{"MPL-2.0", "Mozilla Public License Version 2.0"},
	{"Apache-2.0", "Apache License\n                           Version 2.0"},
	{"Apache-2.0", "Apache License, Version 2.0"},
	{"MIT", "Permission is hereby granted, free of charge"},
	{"BSD-3-Clause", "Neither the name of"},
	{"BSD-2-Clause", "Redistributions in binary form must reproduce"},
	{"ISC", "Permission to use, copy, modify, and/or distribute this software"},
	{"Unlicense", "This is free and unencumbered software released into the public domain"},
	{"0BSD", "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted."},
}

var licenseFiles = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "LICENCE", "LICENSE-MIT", "LICENSE-APACHE"}

func (l *Library) detectLicense(ctx context.Context, r *Repo) string {
	if !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		return ""
	}
	for _, f := range licenseFiles {
		b, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", f, 256<<10)
		if err != nil {
			continue
		}
		text := string(b)
		for _, h := range licenseHints {
			if strings.Contains(text, h.needle) {
				return h.id
			}
		}
	}
	return ""
}

// ---- Relation suggestions (KONZEPT 6.7, T-2.5) ----

// addSuggestion adds a suggested relation unless the same type and target
// is already known in any state (rejected ones are never offered again).
func addSuggestion(r *repometa.Repo, typ, target, source string) bool {
	for _, rel := range r.Relations {
		if rel.Type == typ && strings.EqualFold(rel.Target, target) {
			return false
		}
	}
	if strings.EqualFold(target, r.Source.Key()) || target == r.ID {
		return false
	}
	r.Relations = append(r.Relations, repometa.Relation{Type: typ, Target: target, State: repometa.RelationSuggested, Source: source})
	return true
}

// confirmRelation turns a suggestion into a confirmed relation (submodules
// are facts, not guesses).
func confirmRelation(r *repometa.Repo, typ, target string) {
	for i, rel := range r.Relations {
		if rel.Type == typ && strings.EqualFold(rel.Target, target) && rel.State == repometa.RelationSuggested {
			r.Relations[i].State = repometa.RelationConfirmed
		}
	}
}

var (
	linkRe     = regexp.MustCompile(`https?://([A-Za-z0-9.-]+(?::\d+)?)/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)
	wikiLinkRe = regexp.MustCompile(`\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|[^\]]*)?\]\]`)
)

// depFiles are scanned for references to other managed repositories.
var depFiles = []string{"go.mod", "Cargo.toml", "package.json", "pubspec.yaml", "requirements.txt", "pyproject.toml"}

// suggestRelations proposes relations to repositories in the library that
// the README, notes or dependency files mention.
func (l *Library) suggestRelations(ctx context.Context, r *Repo) {
	all, err := l.Store.List(ctx, store.Query{})
	if err != nil || len(all) < 2 {
		return
	}
	byKey := map[string]store.Entry{}
	byName := map[string][]store.Entry{}
	for _, e := range all {
		if e.ID == r.R().ID {
			continue
		}
		byKey[store.FoldKey(e.Host+"/"+e.Owner+"/"+e.Name)] = e
		byKey[store.FoldKey(hostName(e.Host)+"/"+e.Owner+"/"+e.Name)] = e
		byName[strings.ToLower(e.Name)] = append(byName[strings.ToLower(e.Name)], e)
	}
	find := func(text string, typ, source string) {
		for _, m := range linkRe.FindAllStringSubmatch(text, -1) {
			key := store.FoldKey(m[1] + "/" + m[2] + "/" + strings.TrimSuffix(m[3], ".git"))
			if e, ok := byKey[key]; ok {
				addSuggestion(r.R(), typ, e.Key, source)
			}
		}
		// Module paths without scheme (go.mod: github.com/a/b v1.2.3).
		for _, f := range strings.Fields(text) {
			f = strings.Trim(f, `"',;()[]{}`)
			parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(f, "git+https://"), "https://"), "/")
			if len(parts) >= 3 {
				key := store.FoldKey(parts[0] + "/" + parts[1] + "/" + strings.TrimSuffix(parts[2], ".git"))
				if e, ok := byKey[key]; ok {
					addSuggestion(r.R(), typ, e.Key, source)
				}
			}
		}
	}
	if l.Git.HasCommits(ctx, r.Paths.Mirror) {
		if _, readme, err := l.Git.Readme(ctx, r.Paths.Mirror, "HEAD", readmeLimit); err == nil {
			find(string(readme), "related", "readme")
		}
		for _, f := range depFiles {
			if b, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", f, 1<<20); err == nil {
				find(string(b), "depends_on", "dependency")
			}
		}
	}
	if notes, err := os.ReadFile(filepath.Join(r.Paths.Meta, layout.NotesFile)); err == nil {
		find(string(notes), "related", "notes")
		for _, m := range wikiLinkRe.FindAllStringSubmatch(string(notes), -1) {
			name := strings.ToLower(strings.TrimSpace(m[1]))
			if cands := byName[name]; len(cands) == 1 {
				// A wiki link written by the user is a decision, not a guess.
				if addSuggestion(r.R(), "related", cands[0].Key, "wikilink") {
					confirmRelation(r.R(), "related", cands[0].Key)
				}
			} else if e, ok := byKey[store.FoldKey(name)]; ok {
				if addSuggestion(r.R(), "related", e.Key, "wikilink") {
					confirmRelation(r.R(), "related", e.Key)
				}
			}
		}
	}
}

func hostName(h string) string {
	n, _, _ := strings.Cut(h, ":")
	return n
}
