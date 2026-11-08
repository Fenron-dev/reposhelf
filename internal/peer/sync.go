// SPDX-License-Identifier: GPL-3.0-or-later

package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/core"
	"github.com/Fenron-dev/reposhelf/internal/gitcli"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

// field is one part of repo.yaml that is synced field by field (T-8.3).
// Instance-local state (sync timestamps, last error) is never synced.
type field struct {
	name  string
	label string
	get   func(*repometa.Repo) any
	set   func(dst, src *repometa.Repo)
}

var fields = []field{
	{"kind", "Art", func(r *repometa.Repo) any { return r.Kind }, func(d, s *repometa.Repo) { d.Kind = s.Kind }},
	{"fork_of", "Fork von", func(r *repometa.Repo) any { return r.ForkOf }, func(d, s *repometa.Repo) { d.ForkOf = s.ForkOf }},
	{"sync.mode", "Aktualisierung", func(r *repometa.Repo) any { return r.Sync.Mode }, func(d, s *repometa.Repo) { d.Sync.Mode = s.Sync.Mode }},
	{"sync.interval", "Intervall", func(r *repometa.Repo) any { return r.Sync.Interval }, func(d, s *repometa.Repo) { d.Sync.Interval = s.Sync.Interval }},
	{"status_override", "Status festgelegt", func(r *repometa.Repo) any { return r.StatusOverride }, func(d, s *repometa.Repo) { d.StatusOverride = s.StatusOverride }},
	{"include", "Mitsichern", func(r *repometa.Repo) any { return r.Include }, func(d, s *repometa.Repo) { d.Include = s.Include }},
	{"tags", "Schlagwörter", func(r *repometa.Repo) any { return r.Tags }, func(d, s *repometa.Repo) { d.Tags = s.Tags }},
	{"collections", "Sammlungen", func(r *repometa.Repo) any { return r.Collections }, func(d, s *repometa.Repo) { d.Collections = s.Collections }},
	{"relations", "Beziehungen", func(r *repometa.Repo) any { return r.Relations }, func(d, s *repometa.Repo) { d.Relations = s.Relations }},
	{"retention", "Aufbewahrung", func(r *repometa.Repo) any { return r.Retention }, func(d, s *repometa.Repo) { d.Retention = s.Retention }},
	{"buildable", "Baubar halten", func(r *repometa.Repo) any { return r.Buildable }, func(d, s *repometa.Repo) { d.Buildable = s.Buildable }},
	{"license", "Lizenz", func(r *repometa.Repo) any { return r.License }, func(d, s *repometa.Repo) { d.License = s.License }},
}

func render(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSpace(string(b))
}

// Conflict is a field both instances changed differently.
type Conflict struct {
	ID     string    `yaml:"id"`
	Peer   string    `yaml:"peer"`
	RepoID string    `yaml:"repo_id"`
	Key    string    `yaml:"key"`
	Field  string    `yaml:"field"`
	Label  string    `yaml:"label"`
	Local  string    `yaml:"local"`
	Remote string    `yaml:"remote"`
	Base   string    `yaml:"base"`
	Found  time.Time `yaml:"found"`
	// RemoteYAML keeps the peer's whole repo.yaml to apply the choice.
	RemoteYAML string `yaml:"remote_yaml"`
}

type conflictsFile struct {
	Conflicts []Conflict `yaml:"conflicts"`
}

// Conflicts lists open conflicts.
func (m *Manager) Conflicts() []Conflict {
	raw, err := os.ReadFile(m.path(ConflictsFile))
	if err != nil {
		return nil
	}
	f, err := repometa.ParseFile(raw, conflictsFile{})
	if err != nil {
		return nil
	}
	return f.Value.Conflicts
}

func (m *Manager) saveConflicts(list []Conflict) error {
	b, err := repometa.NewFile(conflictsFile{Conflicts: list}).Marshal()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(m.path(ConflictsFile), b, 0o644)
}

// Report tells what a sync did.
type Report struct {
	Peer      string
	Repos     int
	Created   []string
	Updated   []string
	Fetched   int
	Rewritten int
	Assets    int
	Capsules  int
	Conflicts int
	Problems  []string
}

// Summary for the UI.
func (r Report) Summary() string {
	s := fmt.Sprintf("%d Repos geprüft, %d neu, %d Metadaten übernommen, %d Code-Abgleiche", r.Repos, len(r.Created), len(r.Updated), r.Fetched)
	if r.Assets > 0 {
		s += fmt.Sprintf(", %d Release-Dateien", r.Assets)
	}
	if r.Capsules > 0 {
		s += fmt.Sprintf(", %d Kapseln", r.Capsules)
	}
	if r.Conflicts > 0 {
		s += fmt.Sprintf(", %d Konflikte", r.Conflicts)
	}
	if len(r.Problems) > 0 {
		s += fmt.Sprintf(", %d Probleme", len(r.Problems))
	}
	return s
}

// Sync pulls the selected repositories from a peer (T-8.2).
func (m *Manager) Sync(ctx context.Context, peerID, actor string) (Report, error) {
	p, err := m.Peer(peerID)
	if err != nil {
		return Report{}, err
	}
	m.mu.Lock()
	if m.syncing[p.ID] {
		m.mu.Unlock()
		return Report{}, core.ErrBusy
	}
	m.syncing[p.ID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.syncing, p.ID); m.mu.Unlock() }()

	rep := Report{Peer: p.Name}
	var resp reposResponse
	if err := m.postJSON(ctx, p.URL+"/peer/repos", reposRequest{Selections: p.Selections}, &resp, true, p); err != nil {
		return rep, err
	}
	auth := &gitcli.Auth{Token: resp.Token, URLPrefix: urlPrefix(p.URL)}
	for _, pl := range resp.Repos {
		rep.Repos++
		level := m.levelFor(ctx, p, pl)
		if err := m.syncRepo(ctx, p, pl, level, auth, &rep, actor); err != nil {
			rep.Problems = append(rep.Problems, pl.Key+": "+err.Error())
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	summary := rep.Summary()
	_ = m.UpdatePeer(p.ID, func(x *Peer) { x.LastSync, x.LastResult = &now, summary })
	m.Lib.Record(core.Activity{Actor: actor, Action: "peer_sync", Detail: p.Name + ": " + summary})
	return rep, nil
}

// levelFor finds the highest level any selection grants to a repository;
// the peer sent only selected repositories, so a match always exists.
func (m *Manager) levelFor(ctx context.Context, p Peer, pl RepoPayload) string {
	best := LevelMeta
	var r repometa.Repo
	_ = yaml.Unmarshal([]byte(pl.RepoYAML), &r)
	for _, s := range p.Selections {
		match := s.Type == "all" ||
			(s.Type == "repo" && (s.Ref == pl.ID || strings.EqualFold(s.Ref, pl.Key))) ||
			(s.Type == "collection" && slices.Contains(r.Collections, s.Ref))
		if match && levelIndex(s.Level) > levelIndex(best) {
			best = s.Level
		}
	}
	return best
}

func urlPrefix(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	return pu.Scheme + "://" + pu.Host + "/"
}

func (m *Manager) syncRepo(ctx context.Context, p Peer, pl RepoPayload, level string, auth *gitcli.Auth, rep *Report, actor string) error {
	doc, err := repometa.Parse([]byte(pl.RepoYAML))
	if err != nil {
		return err
	}
	remote := doc.Repo
	if remote.ID != pl.ID {
		return fmt.Errorf("ID mismatch")
	}
	local, err := m.Lib.Get(ctx, pl.ID)
	switch {
	case errors.Is(err, core.ErrNotFound):
		if _, err := m.Lib.AdoptRepo(ctx, remote, pl.Upstream, pl.Notes, "peer:"+p.Name); err != nil {
			return err
		}
		rep.Created = append(rep.Created, pl.Key)
		if err := m.saveBase(p.ID, pl.ID, pl.RepoYAML); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		changed, err := m.mergeFields(ctx, p, local, remote, pl, rep)
		if err != nil {
			return err
		}
		if changed {
			rep.Updated = append(rep.Updated, pl.Key)
		}
		if pl.Notes != "" {
			if cur, _ := m.Lib.Notes(ctx, pl.ID); cur == "" {
				_ = m.Lib.WriteNotesRaw(ctx, pl.ID, pl.Notes)
			}
		}
	}
	if levelIndex(level) >= levelIndex(LevelCode) {
		res, err := m.Lib.FetchFromPeer(ctx, pl.ID, p.URL+"/git/"+pl.Key+".git", auth, "peer:"+p.Name)
		if err != nil {
			return fmt.Errorf("code: %w", err)
		}
		if res.Changed() {
			rep.Fetched++
		}
		rep.Rewritten += len(res.Rewritten)
	}
	if levelIndex(level) >= levelIndex(LevelReleases) {
		for _, rel := range pl.Releases {
			n, err := m.Lib.StoreRelease(ctx, pl.ID, rel, func(a core.StoredAsset) (io.ReadCloser, error) {
				q := url.Values{"repo": {pl.ID}, "tag": {rel.Tag}, "name": {a.Name}}
				resp, err := m.do(ctx, http.MethodGet, p.URL+"/peer/asset?"+q.Encode(), nil, true)
				if err != nil {
					return nil, err
				}
				return resp.Body, nil
			})
			rep.Assets += n
			if err != nil {
				return fmt.Errorf("release %s: %w", rel.Tag, err)
			}
		}
	}
	if levelIndex(level) >= levelIndex(LevelCapsules) {
		n, err := m.syncCapsules(ctx, p, pl)
		rep.Capsules += n
		if err != nil {
			return fmt.Errorf("capsules: %w", err)
		}
	}
	return nil
}

// mergeFields applies the peer's changes field by field against the last
// synced state (three-way). Both sides changed → conflict.
func (m *Manager) mergeFields(ctx context.Context, p Peer, local *core.Repo, remote repometa.Repo, pl RepoPayload, rep *Report) (bool, error) {
	baseRaw, haveBase := m.loadBase(p.ID, pl.ID)
	var base repometa.Repo
	if haveBase {
		if d, err := repometa.Parse([]byte(baseRaw)); err == nil {
			base = d.Repo
		} else {
			haveBase = false
		}
	}
	conflicts := m.Conflicts()
	var take []field
	for _, f := range fields {
		lv, rv := render(f.get(local.R())), render(f.get(&remote))
		if lv == rv {
			conflicts = dropConflict(conflicts, p.ID, pl.ID, f.name)
			continue
		}
		bv := ""
		if haveBase {
			bv = render(f.get(&base))
		}
		switch {
		case haveBase && lv == bv:
			take = append(take, f) // only the peer changed it
		case haveBase && rv == bv:
			// only we changed it: keep ours
		default:
			// Both changed (or first contact with different values):
			// show, never overwrite silently.
			if !hasConflict(conflicts, p.ID, pl.ID, f.name) {
				conflicts = append(conflicts, Conflict{ID: repometa.NewID(time.Now())[14:], Peer: p.ID, RepoID: pl.ID, Key: pl.Key,
					Field: f.name, Label: f.label, Local: lv, Remote: rv, Base: bv, Found: time.Now().UTC().Truncate(time.Second), RemoteYAML: pl.RepoYAML})
				rep.Conflicts++
			}
		}
	}
	if err := m.saveConflicts(conflicts); err != nil {
		return false, err
	}
	if len(take) > 0 {
		if _, err := m.Lib.Update(ctx, pl.ID, func(r *repometa.Repo) error {
			for _, f := range take {
				f.set(r, &remote)
			}
			return nil
		}); err != nil {
			return false, err
		}
	}
	return len(take) > 0, m.saveBase(p.ID, pl.ID, pl.RepoYAML)
}

func hasConflict(list []Conflict, peer, repo, f string) bool {
	return slices.ContainsFunc(list, func(c Conflict) bool { return c.Peer == peer && c.RepoID == repo && c.Field == f })
}

func dropConflict(list []Conflict, peer, repo, f string) []Conflict {
	return slices.DeleteFunc(list, func(c Conflict) bool { return c.Peer == peer && c.RepoID == repo && c.Field == f })
}

// Resolve applies the user's choice for a conflict: "remote" takes the
// peer's value, "local" keeps ours.
func (m *Manager) Resolve(ctx context.Context, id, choice, actor string) error {
	list := m.Conflicts()
	i := slices.IndexFunc(list, func(c Conflict) bool { return c.ID == id })
	if i < 0 {
		return fmt.Errorf("conflict %s: %w", id, core.ErrNotFound)
	}
	c := list[i]
	if choice == "remote" {
		d, err := repometa.Parse([]byte(c.RemoteYAML))
		if err != nil {
			return err
		}
		for _, f := range fields {
			if f.name == c.Field {
				if _, err := m.Lib.Update(ctx, c.RepoID, func(r *repometa.Repo) error { f.set(r, &d.Repo); return nil }); err != nil {
					return err
				}
			}
		}
	}
	list = slices.Delete(list, i, i+1)
	m.Lib.Record(core.Activity{Actor: actor, Action: "peer_resolve", RepoID: c.RepoID, Detail: c.Label + ": " + choice})
	return m.saveConflicts(list)
}

func (m *Manager) basePath(peer, repo string) (string, error) {
	return safepath.Join(m.Lib.Layout.Path(layout.DirInternal, BaseDir), strings.ReplaceAll(peer, "-", ""), repo+".yaml")
}

func (m *Manager) saveBase(peer, repo, raw string) error {
	p, err := m.basePath(peer, repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(p, []byte(raw), 0o644)
}

func (m *Manager) loadBase(peer, repo string) (string, bool) {
	p, err := m.basePath(peer, repo)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(p)
	return string(b), err == nil
}

// syncCapsules copies capsule manifests and missing blobs from the peer.
func (m *Manager) syncCapsules(ctx context.Context, p Peer, pl RepoPayload) (int, error) {
	r, err := m.Lib.Get(ctx, pl.ID)
	if err != nil {
		return 0, err
	}
	dir, err := artifact.CapsuleDir(m.Lib.Layout.Root, r.R().Source)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, c := range artifact.ReadCapsules(dir) {
		have[c.Version] = true
	}
	blobs := artifact.BlobsOf(m.Lib.Layout.Root)
	n := 0
	for _, cp := range pl.Capsules {
		if have[cp.Manifest.Version] {
			continue
		}
		for _, f := range cp.Files {
			if _, err := os.Stat(blobs.Path(f.SHA256)); err == nil {
				continue
			}
			resp, err := m.do(ctx, http.MethodGet, p.URL+"/peer/blob/"+f.SHA256, nil, true)
			if err != nil {
				return n, err
			}
			tmp, err := os.CreateTemp("", "reposhelf-blob-*")
			if err != nil {
				resp.Body.Close()
				return n, err
			}
			_, err = io.Copy(tmp, resp.Body)
			resp.Body.Close()
			tmp.Close()
			if err == nil {
				var got artifact.File
				got, _, err = blobs.Put(tmp.Name(), f.Name)
				if err == nil && got.SHA256 != f.SHA256 {
					err = fmt.Errorf("blob %s: checksum mismatch", f.SHA256)
				}
			}
			os.Remove(tmp.Name())
			if err != nil {
				return n, err
			}
		}
		if err := artifact.WriteCapsule(dir, cp.Manifest, cp.Files); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
