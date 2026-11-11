// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
	"github.com/Fenron-dev/reposhelf/internal/store"
)

// SyncReport tells what one sync did.
type SyncReport struct {
	RepoID   string
	Key      string
	Mirror   gitcli.MirrorResult
	Status   string
	Err      error
	Releases ReleaseReport
	Actions  ActionsReport
	Added    []string // repositories added for submodules
	Notes    []string // non-fatal problems (LFS, wiki, metadata …)
	Duration time.Duration
}

// hostDownAfter: a host counts as disturbed only when all its active
// repositories have failed for at least this long (Fero source_health), so
// a GitHub outage is not mistaken for 100 vanished repositories.
const hostDownAfter = 24 * time.Hour

// syncTimeout bounds one sync (large first clones need time).
const syncTimeout = 6 * time.Hour

// Sync brings one repository up to date: git refs (with force-push
// backups), metadata, license, releases, issues, wiki, LFS, submodules,
// snapshots and statistics. Errors of the git step decide the upstream
// status; errors of the extras are reported as notes.
func (l *Library) Sync(ctx context.Context, ref, actor string) (SyncReport, error) {
	start := l.now()
	r, err := l.Get(ctx, ref)
	if err != nil {
		return SyncReport{}, err
	}
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	if r, err = l.load(ctx, r.Paths.Meta); err != nil {
		return SyncReport{}, err
	}
	rep := SyncReport{RepoID: r.R().ID, Key: r.Entry.Key}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	if r.R().Source.Host == repometa.LocalHost {
		l.refreshStats(ctx, r)
		rep.Status = StatusLocal
		return rep, nil
	}

	now := l.now().UTC().Truncate(time.Second)
	src := r.R().Source
	auth := l.Sources.GitAuth(src)
	if _, err := os.Stat(r.Paths.Mirror); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(parentDir(r.Paths.Mirror), dirPerm); err != nil {
			return rep, err
		}
		if err := l.Git.InitBare(ctx, r.Paths.Mirror, src.URL); err != nil {
			return rep, err
		}
	}
	id := r.R().ID
	l.progress(id, "Code wird geholt", 0)
	gctx := gitcli.WithProgress(ctx, func(p int) { l.progress(id, "Code wird geholt", p) })
	rep.Mirror, rep.Err = l.Git.Mirror(gctx, r.Paths.Mirror, src.URL, auth, now)
	l.progress(id, "Metadaten", -1)

	// Metadata from the API (independent of git success: an API 404 is the
	// clearest "gone" signal).
	var apiErr error
	if api, err := l.Sources.API(src.Host); err == nil {
		m, err := api.Metadata(ctx, src)
		apiErr = err
		if err == nil {
			r.Upstream.setMetadata(m, now)
			l.applyMetadata(r, m)
		} else if !errors.Is(err, sources.ErrGone) {
			rep.Notes = append(rep.Notes, "Metadaten: "+err.Error())
		}
	}

	l.updateStatus(ctx, r, rep.Err, apiErr, now)
	rep.Status = effectiveStatus(r.R(), r.Upstream)
	cr := r.R()
	cr.Sync.LastCheck = &now
	if rep.Err == nil {
		cr.Sync.LastSuccess = &now
		cr.Sync.LastError = nil
		if rep.Mirror.Backup != "" {
			r.Upstream.LastBackup = rep.Mirror.Backup
		}
		r.Upstream.DeletedUpstream = rep.Mirror.Deleted
		rep.Notes = append(rep.Notes, l.syncExtras(ctx, r, auth, &rep)...)
		l.finishImport(ctx, r)
		l.progress(id, "Für Programme bereitstellen", -1)
		if missing, err := l.exportHF(ctx, r); err != nil {
			rep.Notes = append(rep.Notes, "Modell-Ordner: "+err.Error())
		} else if missing > 0 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("Modell-Ordner: %d große Dateien fehlen noch (Git LFS)", missing))
		}
	} else {
		msg := rep.Err.Error()
		cr.Sync.LastError = &msg
	}
	l.progress(id, "Statistik", -1)
	l.measure(ctx, r)
	if err := writeUpstream(r.Paths.Meta, r.Upstream); err != nil {
		return rep, err
	}
	if err := l.save(ctx, r); err != nil {
		return rep, err
	}
	rep.Duration = l.now().Sub(start)
	detail := fmt.Sprintf("%d neu, %d aktualisiert, %d umgeschrieben", len(rep.Mirror.New), len(rep.Mirror.Updated), len(rep.Mirror.Rewritten))
	if rep.Err != nil {
		detail = "Fehler: " + rep.Err.Error()
	}
	l.Record(Activity{Actor: actorOr(actor), Action: "sync", RepoID: r.R().ID, Detail: detail})
	return rep, rep.Err
}

// applyMetadata takes over what the user has not decided: the license and
// (for forks found via the API) a suggested relation.
func (l *Library) applyMetadata(r *Repo, m sources.Metadata) {
	cr := r.R()
	if cr.License.SPDX == nil && m.License != "" {
		lic := m.License
		cr.License.SPDX = &lic
	}
	if m.License != "" || cr.License.Checked == nil {
		d := repometa.NewDate(l.now())
		cr.License.Checked = &d
	}
	if m.ForkParent != "" {
		addSuggestion(cr, "fork_of", m.ForkParent, "fork")
	}
}

// updateStatus derives the detected status (KONZEPT 6.2) and keeps
// failing_since for the host health rule.
func (l *Library) updateStatus(ctx context.Context, r *Repo, gitErr, apiErr error, now time.Time) {
	up := &r.Upstream
	if gitErr == nil {
		up.FailingSince, up.ErrorKind, up.HostDown = nil, "", false
		up.Status = StatusOK
		if up.Metadata != nil && up.Metadata.Archived {
			up.Status = StatusArchived
		}
		return
	}
	if up.FailingSince == nil {
		up.FailingSince = &now
	}
	gone := errors.Is(apiErr, sources.ErrGone) || errors.Is(gitErr, gitcli.ErrGone)
	switch {
	case errors.Is(gitErr, gitcli.ErrAuth):
		up.ErrorKind = "auth"
	case gone:
		up.ErrorKind = "gone"
	default:
		up.ErrorKind = "network"
	}
	up.HostDown = l.hostDown(ctx, r.R().Source.Host, r.R().ID, *up.FailingSince, now)
	switch {
	case up.HostDown:
		// The whole host fails: do not conclude that this repo is gone.
		up.Status = StatusDegraded
	case gone && now.Sub(*up.FailingSince) >= 0:
		up.Status = StatusGone
	default:
		up.Status = StatusDegraded
	}
}

// hostDown reports whether every other active repository of host has been
// failing for at least hostDownAfter as well (and this one too).
func (l *Library) hostDown(ctx context.Context, host, selfID string, selfSince, now time.Time) bool {
	if now.Sub(selfSince) < hostDownAfter {
		return false
	}
	entries, err := l.Store.List(ctx, store.Query{Host: host})
	if err != nil {
		return false
	}
	others := 0
	for _, e := range entries {
		if e.ID == selfID || e.SyncMode == string(repometa.SyncManual) {
			continue
		}
		others++
		if e.FailingSince == nil || now.Sub(*e.FailingSince) < hostDownAfter {
			return false
		}
	}
	return others > 0
}

// syncExtras runs the optional parts after a successful git sync.
func (l *Library) syncExtras(ctx context.Context, r *Repo, auth *gitcli.Auth, rep *SyncReport) []string {
	var notes []string
	cr := r.R()
	if cr.Include.LFS && l.lfs && l.hasLFS(ctx, r) {
		if err := l.Git.FetchLFS(ctx, r.Paths.Mirror, auth); err != nil {
			notes = append(notes, "LFS: "+err.Error())
		}
	} else if cr.Include.LFS && !l.lfs && l.hasLFS(ctx, r) {
		notes = append(notes, "LFS: git-lfs ist nicht installiert, große Dateien fehlen")
	}
	if cr.Include.Releases {
		l.progress(cr.ID, "Releases", -1)
		rr, err := l.syncReleases(ctx, r)
		rep.Releases = rr
		if err != nil {
			notes = append(notes, "Releases: "+err.Error())
		}
	}
	if cr.Include.Actions {
		l.progress(cr.ID, "Actions-Artefakte", -1)
		ar, err := l.syncActions(ctx, r)
		rep.Actions = ar
		if err != nil {
			notes = append(notes, "Actions-Artefakte: "+err.Error())
		}
	}
	if cr.Include.Issues {
		l.progress(cr.ID, "Issues", -1)
		if err := l.syncIssues(ctx, r); err != nil && !errors.Is(err, sources.ErrNoAPI) {
			notes = append(notes, "Issues: "+err.Error())
		}
	}
	if cr.Include.Wiki {
		l.progress(cr.ID, "Wiki", -1)
		if err := l.syncWiki(ctx, r, auth); err != nil {
			notes = append(notes, "Wiki: "+err.Error())
		}
	}
	if cr.Include.Submodules {
		added, err := l.syncSubmodules(ctx, r)
		rep.Added = added
		if err != nil {
			notes = append(notes, "Submodule: "+err.Error())
		}
	}
	if err := l.snapshot(ctx, r); err != nil {
		notes = append(notes, "Snapshot: "+err.Error())
	}
	if cr.License.SPDX == nil {
		if id := l.detectLicense(ctx, r); id != "" {
			cr.License.SPDX = &id
			d := repometa.NewDate(l.now())
			cr.License.Checked = &d
		}
	}
	l.suggestRelations(ctx, r)
	l.detectModels(ctx, r)
	return notes
}

// hasLFS reports whether .gitattributes at HEAD uses LFS.
func (l *Library) hasLFS(ctx context.Context, r *Repo) bool {
	b, err := l.Git.ReadFile(ctx, r.Paths.Mirror, "HEAD", ".gitattributes", 1<<20)
	return err == nil && containsFold(string(b), "filter=lfs")
}

// measure refreshes statistics of the local copy.
func (l *Library) measure(ctx context.Context, r *Repo) {
	st := &r.Upstream.Stats
	st.SizeBytes = gitcli.DirSize(r.Paths.Mirror) + gitcli.DirSize(r.Paths.Releases)
	if dir, err := l.actionsDir(r); err == nil {
		st.SizeBytes += gitcli.DirSize(dir)
	}
	if br, err := l.Git.Branches(ctx, r.Paths.Mirror); err == nil {
		st.Branches = len(br)
	}
	if vs, err := l.Git.Versions(ctx, r.Paths.Mirror); err == nil {
		st.Versions = len(vs)
	}
	st.Releases = l.countReleases(r)
	if log, err := l.Git.Log(ctx, r.Paths.Mirror, "HEAD", 1); err == nil && len(log) == 1 {
		t := log[0].Time.UTC()
		st.LastCommit = &t
	}
	if name, _, err := l.Git.Readme(ctx, r.Paths.Mirror, "HEAD", 1); err == nil || name != "" {
		st.Readme = name
	}
}

// refreshStats measures and saves (local repositories, after pushes).
func (l *Library) refreshStats(ctx context.Context, r *Repo) {
	l.Git.FixHead(ctx, r.Paths.Mirror)
	l.measure(ctx, r)
	if r.R().Source.Host == repometa.LocalHost {
		r.Upstream.Status = StatusLocal
	}
	if err := writeUpstream(r.Paths.Meta, r.Upstream); err != nil {
		l.Log.Warn("write upstream", "err", err)
	}
	if err := l.save(ctx, r); err != nil {
		l.Log.Warn("save after stats", "err", err)
	}
}

// RefreshAfterPush updates statistics after a push via Git HTTP.
func (l *Library) RefreshAfterPush(ctx context.Context, ref, actor string) {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return
	}
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	l.refreshStats(ctx, r)
	l.Record(Activity{Actor: actor, Action: "push", RepoID: r.R().ID})
	if r.R().Sync.PushUpstream {
		l.forwardPush(ctx, r)
	}
}

// forwardPush passes branches and tags on to the original (D-029): never
// forced and never deleting there; refs the original has moved on are
// rejected and reported.
func (l *Library) forwardPush(ctx context.Context, r *Repo) {
	src := r.R().Source
	err := l.Git.PushAll(ctx, r.Paths.Mirror, src.URL, l.Sources.GitAuth(src))
	now := l.now().UTC()
	if err != nil {
		r.Upstream.ForwardError = err.Error()
		l.Record(Activity{Actor: "push-forward", Action: "push-forward-failed", RepoID: r.R().ID, Detail: err.Error()})
	} else {
		r.Upstream.Forwarded, r.Upstream.ForwardError = &now, ""
		l.Record(Activity{Actor: "push-forward", Action: "push-forwarded", RepoID: r.R().ID, Detail: src.URL})
	}
	if err := writeUpstream(r.Paths.Meta, r.Upstream); err != nil {
		l.Log.Warn("write upstream", "err", err)
	}
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
}
