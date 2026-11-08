// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Fenron-dev/reposhelf/internal/auth"
	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/githttp"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// ---- Library (T-1.9) ----

type libraryData struct {
	Query    store.Query
	View     string
	Entries  []store.Entry
	Groups   []group
	Kinds    map[string]int
	Statuses map[string]int
	Build    int
	Total    int
	Filtered bool
	Review   core.StartupReview
	ShowRev  bool
	Busy     map[string]bool
}

type group struct {
	Name    string
	Entries []store.Entry
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	d := libraryData{
		Query: store.Query{Text: q.Get("q"), Kind: q.Get("kind"), Tag: q.Get("tag"), Status: q.Get("status"),
			Collection: q.Get("collection"), Language: q.Get("language"), License: q.Get("license"),
			Buildable: q.Get("buildable") == "1", Sort: q.Get("sort")},
		View: q.Get("view"), Kinds: map[string]int{}, Statuses: map[string]int{}, Busy: map[string]bool{},
	}
	if d.View == "" {
		d.View = "list"
	}
	if d.Query.Sort == "" {
		d.Query.Sort = store.SortUpdated
	}
	var err error
	d.Entries, err = s.Lib.List(ctx, d.Query)
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	all, _ := s.Lib.List(ctx, store.Query{})
	d.Total = len(all)
	for _, e := range all {
		d.Kinds[e.Kind]++
		d.Statuses[e.Status]++
		if e.Buildable {
			d.Build++
		}
	}
	for _, e := range d.Entries {
		d.Busy[e.ID] = s.Lib.IsBusy(e.ID)
	}
	d.Filtered = d.Query != (store.Query{Sort: d.Query.Sort})
	if d.View == "folders" {
		byOwner := map[string][]store.Entry{}
		for _, e := range d.Entries {
			k := e.Host + " / " + e.Owner
			byOwner[k] = append(byOwner[k], e)
		}
		for k, v := range byOwner {
			d.Groups = append(d.Groups, group{Name: k, Entries: v})
		}
		sort.Slice(d.Groups, func(i, j int) bool { return strings.ToLower(d.Groups[i].Name) < strings.ToLower(d.Groups[j].Name) })
	}
	// Cleanup review at start (T-3.3): once per run, admins only.
	if tok, _ := s.Identity(r); tok.Has(auth.Admin) {
		s.reviewMu.Lock()
		reviewed := s.reviewed
		s.reviewMu.Unlock()
		if !reviewed {
			if rv, err := s.Lib.Review(ctx); err == nil && (len(rv.Pending) > 0 || len(rv.Due) > 0) {
				d.Review, d.ShowRev = rv, true
			}
		}
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "results" {
		s.partial(w, "results", d)
		return
	}
	s.render(w, r, "library", "Bibliothek", "library", d)
}

// searchJSON feeds the command palette (⌘K, T-2.7).
func (s *Server) searchJSON(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	type hit struct {
		Title, Sub, URL, Kind string
	}
	var hits []hit
	if q != "" {
		entries, _ := s.Lib.List(r.Context(), store.Query{Text: q, Limit: 12})
		for _, e := range entries {
			hits = append(hits, hit{Title: e.Owner + " / " + e.Name, Sub: e.Description, URL: "/repo/" + e.ID, Kind: "repo"})
		}
		if tags, err := s.Lib.Store.Tags(r.Context()); err == nil {
			for _, t := range tags {
				if strings.Contains(strings.ToLower(t.Name), strings.ToLower(q)) && len(hits) < 20 {
					hits = append(hits, hit{Title: "Schlagwort " + t.Name, Sub: fmt.Sprintf("%d Repos", t.Count), URL: "/?tag=" + t.Name + "/*", Kind: "tag"})
				}
			}
		}
		if cols, err := s.Lib.Collections(r.Context()); err == nil {
			for _, c := range cols {
				if strings.Contains(strings.ToLower(c.Name), strings.ToLower(q)) {
					hits = append(hits, hit{Title: "Sammlung " + c.Name, Sub: fmt.Sprintf("%d Repos", c.Size), URL: "/collections/" + c.ID, Kind: "collection"})
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hits)
}

// eventsSSE pushes change notifications so lists refresh live.
func (s *Server) eventsSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.Lib.Subscribe()
	defer cancel()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	fmt.Fprint(w, ": hallo\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) jobsPartial(w http.ResponseWriter, r *http.Request) {
	jobs := s.Lib.Jobs()
	s.partial(w, "jobs", jobs)
}

// ---- Repository detail (T-1.10) ----

type repoData struct {
	Repo        *core.Repo
	Tab         string
	CloneURL    string
	Readme      template.HTML
	ReadmeName  string
	Ref         string
	Path        string
	Tree        []gitcli.TreeEntry
	Crumbs      []crumb
	File        *fileView
	Branches    []gitcli.Ref
	Versions    []gitcli.Ref
	Releases    []core.StoredRelease
	Issues      []core.IssueSummary
	Notes       string
	NotesHTML   template.HTML
	YAML        string
	Commits     []gitcli.Commit
	Collections []core.Collection
	Retention   core.Retention
	Busy        bool
	Marked      bool
	Activity    []core.Activity
	Names       map[string]string // repo ID/key → display name for backlinks
	Backups     []gitcli.Backup
	RelTypes    []string
	CanWrite    bool
	CanAdmin    bool
	CanBuild    bool
	Builds      any
	NoteLinks   []string
}

type crumb struct{ Name, URL string }

type fileView struct {
	Name, Path string
	Size       int
	Text       string
	Lines      []string
	Binary     bool
	Image      bool
	Markdown   template.HTML
	TooLarge   bool
}

const maxView = 1 << 20

func (s *Server) loadRepo(w http.ResponseWriter, r *http.Request) (*core.Repo, bool) {
	repo, err := s.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failErr(w, r, err)
		return nil, false
	}
	return repo, true
}

func (s *Server) baseRepoData(r *http.Request, repo *core.Repo) repoData {
	tok, _ := s.Identity(r)
	d := repoData{Repo: repo, Busy: s.Lib.IsBusy(repo.R().ID), Marked: s.Lib.IsMarked(repo.R().ID),
		Retention: s.Lib.EffectiveRetention(repo), Names: map[string]string{},
		RelTypes: repometa.KnownRelationTypes, CanWrite: tok.Has(auth.Write), CanAdmin: tok.Has(auth.Admin), CanBuild: tok.Has(auth.Build)}
	d.CloneURL = githttp.CloneURL(s.CloneBase(r), repo.Entry.Key)
	if s.Ext.Builds != nil {
		d.Builds = s.Ext.Builds.Panel(r, repo.R().ID)
	}
	d.NoteLinks = s.Lib.NoteBacklinks(r.Context(), repo.R().ID)
	d.Collections, _ = s.Lib.Collections(r.Context())
	for _, bl := range repo.Backlinks {
		if e, err := s.Lib.Store.Get(r.Context(), bl.From); err == nil {
			d.Names[bl.From] = e.Owner + " / " + e.Name
		}
	}
	for _, rel := range repo.R().Relations {
		if e, err := s.Lib.Store.Get(r.Context(), rel.Target); err == nil {
			d.Names[rel.Target] = e.ID
		}
	}
	if repo.R().ForkOf != nil {
		if e, err := s.Lib.Store.Get(r.Context(), *repo.R().ForkOf); err == nil {
			d.Names[*repo.R().ForkOf] = e.ID
		}
	}
	return d
}

func (s *Server) repoPage(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	d := s.baseRepoData(r, repo)
	d.Tab = r.URL.Query().Get("tab")
	if d.Tab == "" {
		d.Tab = "overview"
	}
	mirror := repo.Paths.Mirror
	hasCommits := s.Lib.Git.HasCommits(ctx, mirror)
	switch d.Tab {
	case "overview":
		if hasCommits {
			if name, b, err := s.Lib.Git.Readme(ctx, mirror, "HEAD", maxView); err == nil && name != "" {
				d.ReadmeName = name
				if isMarkdown(name) {
					d.Readme = s.renderMarkdown(b, repo.R().ID, "HEAD", "")
				} else {
					d.Readme = template.HTML("<pre>" + template.HTMLEscapeString(string(b)) + "</pre>")
				}
			}
			d.Commits, _ = s.Lib.Git.Log(ctx, mirror, "HEAD", 5)
		}
	case "code":
		s.fillTree(r, &d, "", r.URL.Query().Get("ref"))
	case "versions":
		d.Versions, _ = s.Lib.Git.Versions(ctx, mirror)
		d.Branches, _ = s.Lib.Git.Branches(ctx, mirror)
		d.Releases, _ = s.Lib.Releases(ctx, repo.R().ID)
		d.Backups, _ = s.Lib.Git.Backups(ctx, mirror)
	case "issues":
		d.Issues, _ = s.Lib.Issues(ctx, repo.R().ID)
	case "notes":
		d.Notes, _ = s.Lib.Notes(ctx, repo.R().ID)
		d.NotesHTML = s.renderMarkdown([]byte(d.Notes), "", "", "")
	case "yaml":
		b, _ := os.ReadFile(repo.Paths.RepoFile)
		d.YAML = string(b)
	case "activity":
		d.Activity = s.Lib.RecentActivity(100, repo.R().ID)
	}
	s.render(w, r, "repo", repo.Entry.Owner+" / "+repo.Entry.Name, "library", d)
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".md" || ext == ".markdown" || ext == ".mdown"
}

func (s *Server) fillTree(r *http.Request, d *repoData, p, ref string) {
	if ref == "" {
		ref = "HEAD"
	}
	d.Ref, d.Path, d.Tab = ref, strings.Trim(p, "/"), "code"
	d.Tree, _ = s.Lib.Git.ListTree(r.Context(), d.Repo.Paths.Mirror, ref, d.Path)
	d.Crumbs = crumbs(d.Repo.R().ID, d.Path, ref)
	d.Branches, _ = s.Lib.Git.Branches(r.Context(), d.Repo.Paths.Mirror)
	d.Versions, _ = s.Lib.Git.Versions(r.Context(), d.Repo.Paths.Mirror)
}

func crumbs(id, p, ref string) []crumb {
	out := []crumb{{Name: "Wurzel", URL: "/repo/" + id + "/tree/?ref=" + ref}}
	if p == "" {
		return out
	}
	acc := ""
	for _, seg := range strings.Split(p, "/") {
		acc = strings.TrimPrefix(acc+"/"+seg, "/")
		out = append(out, crumb{Name: seg, URL: "/repo/" + id + "/tree/" + acc + "?ref=" + ref})
	}
	return out
}

func (s *Server) repoTree(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	d := s.baseRepoData(r, repo)
	s.fillTree(r, &d, r.PathValue("path"), r.URL.Query().Get("ref"))
	s.render(w, r, "repo", repo.Entry.Owner+" / "+repo.Entry.Name, "library", d)
}

func (s *Server) repoBlob(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	d := s.baseRepoData(r, repo)
	p := strings.Trim(r.PathValue("path"), "/")
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = "HEAD"
	}
	d.Tab, d.Ref, d.Path = "code", ref, p
	d.Crumbs = crumbs(repo.R().ID, p, ref)
	fv := &fileView{Name: path.Base(p), Path: p}
	b, err := s.Lib.Git.ReadFile(r.Context(), repo.Paths.Mirror, ref, p, maxView)
	switch {
	case errors.Is(err, gitcli.ErrTooLarge):
		fv.TooLarge = true
	case err != nil:
		s.failErr(w, r, fmt.Errorf("%s: %w", p, core.ErrNotFound))
		return
	default:
		fv.Size = len(b)
		switch {
		case isImage(p):
			fv.Image = true
		case !utf8.Valid(b) || slices.Contains(b, 0):
			fv.Binary = true
		case isMarkdown(p):
			fv.Markdown = s.renderMarkdown(b, repo.R().ID, ref, path.Dir(p))
			fv.Text = string(b)
		default:
			fv.Text = string(b)
			fv.Lines = strings.Split(strings.TrimSuffix(fv.Text, "\n"), "\n")
		}
	}
	d.File = fv
	s.render(w, r, "repo", fv.Name+" – "+repo.Entry.Name, "library", d)
}

func isImage(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico":
		return true
	}
	return false
}

// repoRaw serves file content. Only images are served with their type;
// everything else as plain text or download, so foreign HTML/SVG cannot run
// scripts in our origin.
func (s *Server) repoRaw(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = "HEAD"
	}
	p := strings.Trim(r.PathValue("path"), "/")
	b, err := s.Lib.Git.ReadFile(r.Context(), repo.Paths.Mirror, ref, p, 50<<20)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "text/plain; charset=utf-8"
	if isImage(p) {
		ct = mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	} else if !utf8.Valid(b) {
		ct = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment; filename=\""+path.Base(p)+"\"")
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	w.Write(b)
}

func (s *Server) repoAsset(w http.ResponseWriter, r *http.Request) {
	p, err := s.Lib.AssetPath(r.Context(), r.PathValue("id"), r.URL.Query().Get("tag"), r.URL.Query().Get("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(r.URL.Query().Get("name"), `"`, "")+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

func (s *Server) actor(r *http.Request) string {
	tok, _ := s.Identity(r)
	if tok.ID == auth.Local.ID {
		return "web"
	}
	return "web:" + tok.Name
}

func (s *Server) repoSync(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.loadRepo(w, r)
	if !ok {
		return
	}
	s.Lib.Enqueue(repo.R().ID, repo.Entry.Key, s.actor(r))
	redirect(w, r, withMsg("/repo/"+repo.R().ID, "ok", "Abgleich gestartet – die Seite aktualisiert sich."))
}

func (s *Server) repoTags(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	repo, err := s.Lib.SetTags(r.Context(), r.PathValue("id"), splitList(r.Form.Get("tags")), s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, "/repo/"+repo.R().ID)
}

func (s *Server) repoCollections(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	repo, err := s.Lib.SetCollections(r.Context(), r.PathValue("id"), r.Form["collection"], s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, "/repo/"+repo.R().ID)
}

func (s *Server) repoRelation(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.PathValue("id")
	typ, target := strings.TrimSpace(r.Form.Get("type")), strings.TrimSpace(r.Form.Get("target"))
	var err error
	switch r.Form.Get("action") {
	case "remove":
		_, err = s.Lib.RemoveRelation(r.Context(), id, typ, target, s.actor(r))
	case "reject":
		_, err = s.Lib.SetRelation(r.Context(), id, typ, target, repometa.RelationRejected, s.actor(r))
	default:
		_, err = s.Lib.SetRelation(r.Context(), id, typ, target, repometa.RelationConfirmed, s.actor(r))
	}
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, "/repo/"+id)
}

func (s *Server) repoNotes(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.PathValue("id")
	if err := s.Lib.WriteNotes(r.Context(), id, r.Form.Get("notes"), s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+id+"?tab=notes", "ok", "Notizen gespeichert."))
}

func (s *Server) repoSettings(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.Form
	set := core.Settings{
		Kind: repometa.Kind(f.Get("kind")),
		Mode: repometa.SyncMode(f.Get("mode")),
		Include: repometa.Include{Releases: f.Get("releases") == "on", LFS: f.Get("lfs") == "on",
			Submodules: f.Get("submodules") == "on", Issues: f.Get("issues") == "on", Wiki: f.Get("wiki") == "on"},
		Buildable: f.Get("buildable") == "on",
	}
	if v := f.Get("interval"); v != "" && set.Mode == repometa.SyncFollow {
		d, err := repometa.ParseDuration(v)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "Intervall: "+err.Error())
			return
		}
		set.Interval = &d
	}
	if v := f.Get("override"); v != "" {
		st := repometa.UpstreamStatus(v)
		set.StatusOverride = &st
	}
	repo, err := s.Lib.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	set.Retention = repo.R().Retention
	set.Retention.ReleasesKeep = optInt(f.Get("releases_keep"))
	set.Retention.Snapshots.Every = optDuration(f.Get("snapshots_every"))
	set.Retention.Snapshots.KeepFor = optDuration(f.Get("snapshots_keep_for"))
	set.Retention.ForcePushBackups.KeepFor = optDuration(f.Get("backups_keep_for"))
	if _, err := s.Lib.UpdateSettings(r.Context(), repo.R().ID, set, s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+repo.R().ID+"?tab=settings", "ok", "Einstellungen gespeichert."))
}

func optInt(s string) *int {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

func optDuration(s string) *repometa.Duration {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	d, err := repometa.ParseDuration(s)
	if err != nil {
		return nil
	}
	return &d
}

func (s *Server) repoPin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.PathValue("id")
	if _, err := s.Lib.Pin(r.Context(), id, r.Form.Get("version"), r.Form.Get("pin") != "0", s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, "/repo/"+id+"?tab=versions")
}

func (s *Server) repoFork(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	fork, err := s.Lib.Fork(r.Context(), r.PathValue("id"), r.Form.Get("owner"), r.Form.Get("name"), s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+fork.R().ID, "ok", "Fork angelegt – du kannst jetzt hineinpushen."))
}

func (s *Server) repoMark(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.PathValue("id")
	mark := r.Form.Get("mark") != "0"
	if err := s.Lib.MarkForDeletion(r.Context(), id, mark, s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	msg := "Zum Löschen vorgemerkt – du entscheidest in der Lösch-Prüfung."
	if !mark {
		msg = "Vormerkung aufgehoben."
	}
	redirect(w, r, withMsg("/repo/"+id, "ok", msg))
}

// ---- Add (T-1.11) ----

type addData struct {
	URL         string
	Preview     *core.Preview
	Collections []core.Collection
	Error       string
	Intervals   []string
	Defaults    config.Config
}

func (s *Server) addPage(w http.ResponseWriter, r *http.Request) {
	d := addData{URL: r.URL.Query().Get("url"), Intervals: []string{"1h", "6h", "24h", "7d"}, Defaults: s.Lib.Config()}
	d.Collections, _ = s.Lib.Collections(r.Context())
	if d.URL != "" {
		if pv, err := s.Lib.Preview(r.Context(), d.URL); err == nil {
			d.Preview = &pv
		} else {
			d.Error = ErrText(err)
		}
	}
	s.render(w, r, "add", "Repo hinzufügen", "library", d)
}

func (s *Server) addPreview(w http.ResponseWriter, r *http.Request) {
	d := addData{URL: strings.TrimSpace(r.URL.Query().Get("url"))}
	if d.URL != "" {
		if pv, err := s.Lib.Preview(r.Context(), d.URL); err == nil {
			d.Preview = &pv
		} else {
			d.Error = ErrText(err)
		}
	}
	s.partial(w, "add-preview", d)
}

func (s *Server) addSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.Form
	req := core.AddRequest{
		URL: f.Get("url"), Kind: repometa.Kind(f.Get("kind")), Mode: repometa.SyncMode(f.Get("mode")),
		Tags: splitList(f.Get("tags")), Collections: f["collection"], Buildable: f.Get("buildable") == "on",
		Notes: f.Get("notes"), Actor: s.actor(r),
	}
	if v := f.Get("interval"); v != "" && req.Mode == repometa.SyncFollow {
		d, err := repometa.ParseDuration(v)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, "Intervall: "+err.Error())
			return
		}
		req.Interval = &d
	}
	inc := repometa.Include{Releases: f.Get("releases") == "on", LFS: f.Get("lfs") == "on",
		Submodules: f.Get("submodules") == "on", Issues: f.Get("issues") == "on", Wiki: f.Get("wiki") == "on"}
	req.Include = &inc
	repo, err := s.Lib.Add(r.Context(), req)
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+repo.R().ID, "ok", "Aufgenommen – der erste Abgleich läuft im Hintergrund."))
}

func (s *Server) createLocal(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	repo, err := s.Lib.CreateLocal(r.Context(), r.Form.Get("owner"), r.Form.Get("name"), r.Form.Get("description"), splitList(r.Form.Get("tags")), s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+repo.R().ID, "ok", "Projekt angelegt. Mit „git push“ an die Klon-Adresse füllst du es."))
}

// maxUpload bounds ZIP uploads.
const maxUpload = 4 << 30

func (s *Server) importZip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Upload: "+err.Error())
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, hdr, err := r.FormFile("zip")
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Bitte eine ZIP-Datei wählen.")
		return
	}
	defer file.Close()
	zr, err := zip.NewReader(file, hdr.Size)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Keine gültige ZIP-Datei: "+err.Error())
		return
	}
	repo, err := s.Lib.ImportZIP(r.Context(), zr, r.FormValue("owner"), r.FormValue("name"), r.FormValue("description"), splitList(r.FormValue("tags")), s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/repo/"+repo.R().ID, "ok", "Aus ZIP angelegt."))
}

// ---- Tags, collections, graph ----

func (s *Server) tagsPage(w http.ResponseWriter, r *http.Request) {
	tree, err := s.Lib.TagTree(r.Context())
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	s.render(w, r, "tags", "Schlagwörter", "tags", tree)
}

func (s *Server) tagsRename(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	n, err := s.Lib.RenameTag(r.Context(), r.Form.Get("from"), r.Form.Get("to"), s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/tags", "ok", fmt.Sprintf("In %d Repos umbenannt.", n)))
}

type collectionData struct {
	C       core.Collection
	Entries []store.Entry
	New     bool
	Fields  []string
	Ops     []string
	Rules   []core.SmartRule
	Busy    map[string]bool
}

func (s *Server) collectionPage(w http.ResponseWriter, r *http.Request) {
	c, err := s.Lib.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	entries, err := s.Lib.CollectionMembers(r.Context(), c.ID)
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	s.render(w, r, "collection", c.Name, "col:"+c.ID, collectionData{C: c, Entries: entries, Busy: map[string]bool{}})
}

func (s *Server) collectionEdit(w http.ResponseWriter, r *http.Request) {
	d := collectionData{New: r.PathValue("id") == "", Ops: core.SmartOps}
	for f := range core.SmartFields {
		d.Fields = append(d.Fields, f)
	}
	sort.Strings(d.Fields)
	if !d.New {
		c, err := s.Lib.Collection(r.Context(), r.PathValue("id"))
		if err != nil {
			s.failErr(w, r, err)
			return
		}
		d.C = c
	} else if r.URL.Query().Get("smart") == "1" {
		d.C.Smart = &core.SmartFilter{Logic: "and"}
	}
	if d.C.Smart != nil {
		d.Rules = d.C.Smart.Rules
	}
	for len(d.Rules) < 5 && d.C.Smart != nil {
		d.Rules = append(d.Rules, core.SmartRule{})
	}
	s.render(w, r, "collection-edit", "Sammlung bearbeiten", "", d)
}

func (s *Server) collectionSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.Form
	id := r.PathValue("id")
	if id == "_new" {
		id = slug(f.Get("name"))
	}
	c, _ := s.Lib.Collection(r.Context(), id)
	c.ID, c.Name, c.Description, c.Color, c.Icon = id, strings.TrimSpace(f.Get("name")), f.Get("description"), f.Get("color"), f.Get("icon")
	if c.Name == "" {
		c.Name = id
	}
	c.Retention.ReleasesKeep = optInt(f.Get("releases_keep"))
	c.Retention.SnapshotsEvery = optDuration(f.Get("snapshots_every"))
	c.Retention.ForcePushBackupsKeepFor = optDuration(f.Get("backups_keep_for"))
	c.Retention.CapsulesKeep = optInt(f.Get("capsules_keep"))
	if f.Get("smart") == "1" {
		sf := &core.SmartFilter{Logic: f.Get("logic")}
		fields, ops, vals := f["field"], f["op"], f["value"]
		for i := range fields {
			if fields[i] == "" || i >= len(ops) || i >= len(vals) {
				continue
			}
			var v any = vals[i]
			if strings.Contains(vals[i], ",") && (ops[i] == "in" || ops[i] == "between") {
				parts := splitList(vals[i])
				anyParts := make([]any, len(parts))
				for j := range parts {
					anyParts[j] = parts[j]
				}
				v = anyParts
			}
			sf.Rules = append(sf.Rules, core.SmartRule{Field: fields[i], Op: ops[i], Value: v})
		}
		c.Smart = sf
	} else {
		c.Smart = nil
	}
	if err := s.Lib.SaveCollection(c, s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, "/collections/"+c.ID)
}

func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'ä':
			b.WriteString("ae")
		case r == 'ö':
			b.WriteString("oe")
		case r == 'ü':
			b.WriteString("ue")
		case r == 'ß':
			b.WriteString("ss")
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "sammlung-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	return out
}

func (s *Server) graphPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "graph", "Beziehungen", "graph", nil)
}

func (s *Server) graphJSON(w http.ResponseWriter, r *http.Request) {
	g, err := s.Lib.Graph(r.Context())
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(g)
}

func (s *Server) activityPage(w http.ResponseWriter, r *http.Request) {
	acts := s.Lib.RecentActivity(300, "")
	names := map[string]string{}
	for _, a := range acts {
		if a.RepoID != "" {
			if _, ok := names[a.RepoID]; !ok {
				if e, err := s.Lib.Store.Get(r.Context(), a.RepoID); err == nil {
					names[a.RepoID] = e.Key
				}
			}
		}
	}
	s.render(w, r, "activity", "Verlauf", "activity", map[string]any{"Acts": acts, "Names": names})
}

// ---- Code search (T-9.5, D-021) ----

type codeData struct {
	Q      core.CodeQuery
	Result *core.CodeResult
	Error  string
}

func (s *Server) codePage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := codeData{Q: core.CodeQuery{Pattern: q.Get("q"), Regex: q.Get("regex") == "1", IgnoreCase: q.Get("case") != "1", Tag: q.Get("tag"), Repo: q.Get("repo")}}
	if strings.TrimSpace(d.Q.Pattern) != "" {
		res, err := s.Lib.SearchCode(r.Context(), d.Q)
		if err != nil {
			d.Error = ErrText(err)
		} else {
			d.Result = &res
		}
	}
	s.render(w, r, "code-search", "Code-Suche", "code", d)
}

// ---- Free notes (KONZEPT 6.8) ----

type noteData struct {
	Notes []core.Note
	Note  core.Note
	HTML  template.HTML
	Names map[string]string
	New   bool
}

func (s *Server) noteNames(r *http.Request, ids ...[]string) map[string]string {
	names := map[string]string{}
	for _, list := range ids {
		for _, id := range list {
			if e, err := s.Lib.Store.Get(r.Context(), id); err == nil {
				names[id] = e.Owner + " / " + e.Name
			}
		}
	}
	return names
}

func (s *Server) notesPage(w http.ResponseWriter, r *http.Request) {
	notes, err := s.Lib.FreeNotes(r.Context())
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	var all [][]string
	for _, n := range notes {
		all = append(all, n.Links)
	}
	s.render(w, r, "notes", "Notizen", "notes", noteData{Notes: notes, Names: s.noteNames(r, all...)})
}

func (s *Server) notePage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	n, err := s.Lib.FreeNote(r.Context(), name)
	d := noteData{Note: n}
	if errors.Is(err, core.ErrNotFound) {
		d.New, d.Note.Name = true, name
	} else if err != nil {
		s.failErr(w, r, err)
		return
	}
	d.HTML = s.renderMarkdown([]byte(n.Text), "", "", "")
	d.Names = s.noteNames(r, n.Links)
	s.render(w, r, "note", name, "notes", d)
}

func (s *Server) noteSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "_new" {
		name = strings.TrimSpace(r.Form.Get("name"))
	}
	if err := s.Lib.WriteFreeNote(name, r.Form.Get("text"), s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/notes/"+url.PathEscape(name), "ok", "Gespeichert."))
}

// ---- Cleanup and trash (T-3.3, T-3.4) ----

type cleanupData struct {
	Review    core.StartupReview
	TotalSize int64
	Repos     int
	TrashLen  int
	Days      int
}

func (s *Server) cleanupPage(w http.ResponseWriter, r *http.Request) {
	rv, err := s.Lib.Review(r.Context())
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	d := cleanupData{Review: rv, Days: s.Lib.Config().Retention.TrashDays}
	repos := map[string]bool{}
	for _, it := range rv.Pending {
		d.TotalSize += it.Size
		repos[it.RepoID] = true
	}
	d.Repos = len(repos)
	if tr, err := s.Lib.Trash(); err == nil {
		d.TrashLen = len(tr)
	}
	s.render(w, r, "cleanup", "Lösch-Prüfung", "trash", d)
}

func (s *Server) cleanupSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	ids := r.Form["item"]
	action := r.Form.Get("action")
	if len(r.Form["keep"]) > 0 {
		action = "keep"
	}
	switch action {
	case "later":
		s.reviewMu.Lock()
		s.reviewed = true
		s.reviewMu.Unlock()
		redirect(w, r, withMsg("/", "ok", "Beim nächsten Start wird erneut gefragt."))
		return
	case "keep":
		if len(r.Form["keep"]) > 0 {
			ids = r.Form["keep"]
		}
		if err := s.Lib.KeepItems(r.Context(), ids, s.actor(r)); err != nil {
			s.failErr(w, r, err)
			return
		}
		redirect(w, r, withMsg("/cleanup", "ok", "Behalten – diese Einträge werden nicht mehr vorgeschlagen."))
		return
	}
	n, err := s.Lib.MoveToTrash(r.Context(), ids, s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	s.reviewMu.Lock()
	s.reviewed = true
	s.reviewMu.Unlock()
	redirect(w, r, withMsg("/trash", "ok", fmt.Sprintf("%d Einträge in den Papierkorb verschoben.", n)))
}

func (s *Server) trashPage(w http.ResponseWriter, r *http.Request) {
	list, err := s.Lib.Trash()
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	s.render(w, r, "trash", "Papierkorb", "trash", map[string]any{"List": list, "Days": s.Lib.Config().Retention.TrashDays, "Now": s.Lib.Now()})
}

func (s *Server) trashRestore(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id := r.Form.Get("restore")
	if id == "" {
		id = r.Form.Get("id")
	}
	if err := s.Lib.Restore(r.Context(), id, s.actor(r)); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/trash", "ok", "Wiederhergestellt."))
}

func (s *Server) trashPurge(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("confirm") != "endgültig" {
		s.fail(w, r, http.StatusBadRequest, "Zum endgültigen Löschen „endgültig“ eintippen.")
		return
	}
	n, err := s.Lib.Purge(r.Context(), r.Form["id"], s.actor(r))
	if err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/trash", "ok", fmt.Sprintf("%d Einträge endgültig gelöscht.", n)))
}

// ---- Settings and access ----

type settingsData struct {
	Cfg      config.Config
	Doctor   core.DoctorReport
	DataDir  string
	Warnings []string
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	d := settingsData{Cfg: s.Lib.Config(), DataDir: s.Lib.Layout.Root, Warnings: s.Lib.ConfigWarnings()}
	d.Doctor = s.Lib.Doctor(r.Context())
	s.render(w, r, "settings", "Einstellungen", "settings", d)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f := r.Form
	cfg := s.Lib.Config()
	cfg.Instance.Name = strings.TrimSpace(f.Get("instance"))
	cfg.Server.PublicURL = strings.TrimSpace(f.Get("public_url"))
	cfg.Server.AnonymousRead = f.Get("anonymous_read") == "on"
	if d, err := repometa.ParseDuration(f.Get("default_interval")); err == nil {
		cfg.Sync.DefaultInterval = d
	}
	if d, err := repometa.ParseDuration(f.Get("archived_interval")); err == nil {
		cfg.Sync.ArchivedInterval = d
	}
	if n, err := strconv.Atoi(f.Get("concurrency")); err == nil {
		cfg.Sync.Concurrency = n
	}
	cfg.Sync.Paused = f.Get("paused") == "on"
	if n, err := strconv.Atoi(f.Get("releases_keep")); err == nil {
		cfg.Retention.ReleasesKeep = n
	}
	for key, dst := range map[string]*repometa.Duration{"snapshots_every": &cfg.Retention.SnapshotsEvery, "snapshots_keep_for": &cfg.Retention.SnapshotsKeepFor, "backups_keep_for": &cfg.Retention.ForcePushBackupsKeepFor} {
		v := strings.TrimSpace(f.Get(key))
		if v == "" || v == "0" {
			*dst = 0
		} else if d, err := repometa.ParseDuration(v); err == nil {
			*dst = d
		}
	}
	for key, dst := range map[string]*int{"max_asset_mb": &cfg.Retention.MaxAssetMB, "capsules_keep": &cfg.Retention.CapsulesKeep, "builds_keep": &cfg.Retention.BuildsKeep, "trash_days": &cfg.Retention.TrashDays, "max_parallel": &cfg.Runner.MaxParallel, "min_free_gb": &cfg.Runner.MinFreeGB} {
		if n, err := strconv.Atoi(f.Get(key)); err == nil {
			*dst = n
		}
	}
	cfg.Runner.Enabled = f.Get("runner") == "on"
	cfg.Runner.Confirm = f.Get("confirm") == "on"
	cfg.Runner.Capabilities = splitList(f.Get("capabilities"))
	cfg.Runner.MemoryLimit = strings.TrimSpace(f.Get("memory_limit"))
	cfg.Runner.CPULimit = strings.TrimSpace(f.Get("cpu_limit"))
	if err := s.Lib.SaveConfig(cfg); err != nil {
		s.failErr(w, r, err)
		return
	}
	redirect(w, r, withMsg("/settings", "ok", "Gespeichert."))
}

type accessData struct {
	Tokens    []auth.Token
	NewSecret string
	NewName   string
	Base      string
	ExePath   string
	DataDir   string
	Acts      []core.Activity
}

func (s *Server) accessPage(w http.ResponseWriter, r *http.Request) {
	d := accessData{Tokens: s.Tokens.List(), Base: s.CloneBase(r), DataDir: s.Lib.Layout.Root}
	if exe, err := os.Executable(); err == nil {
		d.ExePath = exe
	}
	for _, a := range s.Lib.RecentActivity(200, "") {
		if strings.HasPrefix(a.Actor, "mcp:") && len(d.Acts) < 30 {
			d.Acts = append(d.Acts, a)
		}
	}
	s.render(w, r, "access", "KI-Zugriff & Tokens", "mcp", d)
}

func (s *Server) tokenCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	tok, secret, err := s.Tokens.Create(r.Form.Get("name"), r.Form["right"])
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Token: "+err.Error())
		return
	}
	s.Lib.Record(core.Activity{Actor: s.actor(r), Action: "token_create", Detail: tok.Name + " " + strings.Join(tok.Rights, ",")})
	d := accessData{Tokens: s.Tokens.List(), NewSecret: secret, NewName: tok.Name, Base: s.CloneBase(r), DataDir: s.Lib.Layout.Root}
	if exe, err := os.Executable(); err == nil {
		d.ExePath = exe
	}
	// Shown once on this response, never stored in clear text.
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, "access", "KI-Zugriff & Tokens", "mcp", d)
}

func (s *Server) tokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.Tokens.Revoke(r.PathValue("id")); err != nil {
		s.fail(w, r, http.StatusNotFound, "Token nicht gefunden.")
		return
	}
	s.Lib.Record(core.Activity{Actor: s.actor(r), Action: "token_revoke", Detail: r.PathValue("id")})
	redirect(w, r, withMsg("/access", "ok", "Token widerrufen."))
}

// ---- Login ----

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login", "Anmelden", "", map[string]string{"Next": r.URL.Query().Get("next")})
}

// ---- helpers ----

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ServeFileSafe serves a file below root (used by extensions).
func ServeFileSafe(w http.ResponseWriter, r *http.Request, root, rel string) {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(p)+"\"")
	http.ServeFile(w, r, p)
}

var _ = layout.DirRepos
