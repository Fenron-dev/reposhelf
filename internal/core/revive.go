// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ---- Reviving a repository (V-5, T-9.24) ----
//
// When an original is gone, the archive is the only copy. Reviving puts
// it back on a host: a new repository is created there with the token of
// that host, every branch and tag (and LFS file) is pushed, and the
// stored releases are recreated with their files. Something leaves
// RepoShelf: only administrators start it, never agents (no MCP tool).

// ReviveRequest describes where a repository is published.
type ReviveRequest struct {
	Host        string // github.com or a Gitea/Forgejo host
	Owner       string // organisation; "" = the token's own account
	Name        string
	Private     bool
	Releases    bool
	Description string
	Actor       string
}

// ReviveState is the progress or result, kept in upstream.json.
type ReviveState struct {
	Target   string    `json:"target"` // host/owner/name
	URL      string    `json:"url,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
	Running  bool      `json:"running,omitempty"`
	Releases int       `json:"releases,omitempty"`
	Files    int       `json:"files,omitempty"`
	Error    string    `json:"error,omitempty"`
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Revive checks the request and starts publishing in the background.
func (l *Library) Revive(ctx context.Context, ref string, req ReviveRequest) error {
	r, err := l.Get(ctx, ref)
	if err != nil {
		return err
	}
	req.Host = strings.ToLower(strings.TrimSpace(req.Host))
	req.Owner = strings.TrimSpace(req.Owner)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = r.R().Source.Name
	}
	if !repoNameRe.MatchString(req.Name) || (req.Owner != "" && !repoNameRe.MatchString(req.Owner)) {
		return fmt.Errorf("%w: Name oder Besitzer ungültig", ErrInvalid)
	}
	if _, err := l.Sources.Publisher(req.Host); err != nil {
		return fmt.Errorf("%w: %s – nur GitHub und Gitea/Forgejo", ErrInvalid, req.Host)
	}
	if l.Tokens.Token(req.Host) == "" {
		return fmt.Errorf("%w: kein Token für %s (Einstellungen → Zugänge)", ErrInvalid, req.Host)
	}
	if !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		return fmt.Errorf("%w: das Repo hat noch keinen Inhalt", ErrInvalid)
	}
	if s := r.Upstream.Revive; s != nil && s.Running {
		return fmt.Errorf("%w: wird bereits hochgeladen", ErrInvalid)
	}
	if req.Description == "" {
		if m := r.Upstream.Metadata; m != nil {
			req.Description = m.Description
		}
	}
	state := &ReviveState{Target: req.Host + "/" + req.Owner + "/" + req.Name, Started: l.now().UTC().Truncate(time.Second), Running: true}
	if err := l.setRevive(r, state); err != nil {
		return err
	}
	l.Record(Activity{Actor: actorOr(req.Actor), Action: "revive-start", RepoID: r.R().ID, Detail: state.Target})
	go l.revive(context.WithoutCancel(ctx), r.R().ID, req, state)
	return nil
}

func (l *Library) setRevive(r *Repo, s *ReviveState) error {
	unlock := l.locks.lock(r.R().ID)
	defer unlock()
	fresh, err := l.load(context.Background(), r.Paths.Meta)
	if err != nil {
		return err
	}
	fresh.Upstream.Revive = s
	if err := writeUpstream(fresh.Paths.Meta, fresh.Upstream); err != nil {
		return err
	}
	l.events.publish(Event{Kind: "repo", RepoID: r.R().ID})
	return nil
}

func (l *Library) revive(ctx context.Context, id string, req ReviveRequest, state *ReviveState) {
	finish := func(r *Repo, err error) {
		state.Running, state.Finished = false, l.now().UTC().Truncate(time.Second)
		detail := state.URL
		if err != nil {
			state.Error, detail = err.Error(), "Fehler: "+err.Error()
		}
		if r != nil {
			_ = l.setRevive(r, state)
		}
		l.Record(Activity{Actor: actorOr(req.Actor), Action: "revive", RepoID: id, Detail: detail})
	}
	r, err := l.Get(ctx, id)
	if err != nil {
		finish(nil, err)
		return
	}
	pub, _ := l.Sources.Publisher(req.Host)
	owner := req.Owner
	if owner == "" {
		if owner, err = pub.Login(ctx); err != nil {
			finish(r, fmt.Errorf("beim Prüfen des Tokens: %w", err))
			return
		}
	}
	cloneURL, webURL, err := pub.CreateRepo(ctx, req.Owner, req.Name, req.Description, req.Private)
	if err != nil {
		finish(r, fmt.Errorf("beim Anlegen des Repos: %w", err))
		return
	}
	state.URL = webURL
	_ = l.setRevive(r, state)
	auth := l.Sources.GitAuth(repometa.Source{Host: req.Host, URL: cloneURL})
	if err := l.Git.PushAll(ctx, r.Paths.Mirror, cloneURL, auth); err != nil {
		finish(r, fmt.Errorf("beim Hochladen des Codes: %w", err))
		return
	}
	if _, err := os.Stat(filepath.Join(r.Paths.Mirror, "lfs", "objects")); err == nil && l.lfs {
		if _, err := l.Git.Run(ctx, r.Paths.Mirror, auth, "lfs", "push", "--all", cloneURL); err != nil {
			finish(r, fmt.Errorf("beim Hochladen der großen Dateien (LFS): %w", err))
			return
		}
	}
	if req.Releases {
		if err := l.reviveReleases(ctx, r, pub, owner, req.Name, state); err != nil {
			finish(r, err)
			return
		}
	}
	finish(r, nil)
}

func (l *Library) reviveReleases(ctx context.Context, r *Repo, pub sources.Publisher, owner, name string, state *ReviveState) error {
	rels, err := l.Releases(ctx, r.R().ID)
	if err != nil {
		return err
	}
	// Oldest first, so the newest ends up as "latest" on the host.
	for i := len(rels) - 1; i >= 0; i-- {
		rel := rels[i]
		id, err := pub.CreateRelease(ctx, owner, name, rel.Release)
		if err != nil {
			return fmt.Errorf("beim Anlegen von Release %s: %w", rel.Tag, err)
		}
		state.Releases++
		for _, a := range rel.Stored {
			p, err := l.AssetPath(ctx, r.R().ID, rel.Tag, a.Name)
			if err != nil {
				return fmt.Errorf("release %s, %s: %w", rel.Tag, a.Name, err)
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			err = pub.UploadAsset(ctx, owner, name, id, a.Name, a.Size, f)
			f.Close()
			if err != nil {
				return fmt.Errorf("beim Hochladen von %s/%s: %w", rel.Tag, a.Name, err)
			}
			state.Files++
		}
		_ = l.setRevive(r, state)
	}
	return nil
}

// PublishHosts lists the hosts a repository can be revived on: those with
// a known API and a token.
func (l *Library) PublishHosts(ctx context.Context) []string {
	hosts, err := l.TokenHosts(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, h := range hosts {
		if h.Source.Kind == sources.FromNone {
			continue
		}
		if _, err := l.Sources.Publisher(h.Host); err == nil {
			out = append(out, h.Host)
		}
	}
	return out
}
