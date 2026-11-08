// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// Free notes live as Markdown files in notes/ (KONZEPT 6.8). Wiki links
// [[Repo]] in them appear as backlinks on the repository page.

// Note is a free note.
type Note struct {
	Name     string // file name without .md
	Text     string
	Modified time.Time
	Links    []string // repository IDs linked via [[…]]
}

func (l *Library) notePath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if err := safepath.CheckSegment(name + ".md"); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return safepath.Join(l.Layout.Path(layout.DirNotes), name+".md")
}

// FreeNotes lists all free notes, newest first, with their repo links.
func (l *Library) FreeNotes(ctx context.Context) ([]Note, error) {
	entries, err := os.ReadDir(l.Layout.Path(layout.DirNotes))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	resolve := l.linkResolver(ctx)
	var out []Note
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || e.IsDir() {
			continue
		}
		n, err := l.readNote(name, resolve)
		if err == nil {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// FreeNote reads one note.
func (l *Library) FreeNote(ctx context.Context, name string) (Note, error) {
	return l.readNote(name, l.linkResolver(ctx))
}

func (l *Library) readNote(name string, resolve func(string) string) (Note, error) {
	p, err := l.notePath(name)
	if err != nil {
		return Note{}, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Note{}, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if err != nil {
		return Note{}, err
	}
	info, _ := os.Stat(p)
	n := Note{Name: name, Text: string(b), Modified: info.ModTime()}
	seen := map[string]bool{}
	for _, m := range wikiLinkRe.FindAllStringSubmatch(n.Text, -1) {
		if id := resolve(strings.TrimSpace(m[1])); id != "" && !seen[id] {
			n.Links = append(n.Links, id)
			seen[id] = true
		}
	}
	return n, nil
}

// WriteFreeNote creates or replaces a note.
func (l *Library) WriteFreeNote(name, text, actor string) error {
	p, err := l.notePath(name)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(p, []byte(text), filePerm); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "note", Detail: name})
	return nil
}

// NoteBacklinks returns the names of free notes that link to the repo.
func (l *Library) NoteBacklinks(ctx context.Context, repoID string) []string {
	notes, err := l.FreeNotes(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range notes {
		for _, id := range n.Links {
			if id == repoID {
				out = append(out, n.Name)
			}
		}
	}
	return out
}

// linkResolver maps a wiki-link target ([[Fero]], [[owner/Fero]],
// [[github.com/owner/Fero]]) to a repository ID; "" if unknown or
// ambiguous.
func (l *Library) linkResolver(ctx context.Context) func(string) string {
	all, _ := l.Store.List(ctx, store.Query{})
	byKey := map[string]string{}
	byName := map[string][]string{}
	for _, e := range all {
		byKey[store.FoldKey(e.Key)] = e.ID
		byKey[store.FoldKey(e.Owner+"/"+e.Name)] = e.ID
		byName[store.FoldKey(e.Name)] = append(byName[store.FoldKey(e.Name)], e.ID)
	}
	return func(target string) string {
		if id, ok := byKey[store.FoldKey(target)]; ok {
			return id
		}
		if ids := byName[store.FoldKey(target)]; len(ids) == 1 {
			return ids[0]
		}
		return ""
	}
}
