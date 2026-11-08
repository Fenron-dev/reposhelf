// SPDX-License-Identifier: GPL-3.0-or-later

// Package sources knows the places repositories come from: it turns a link
// into host/owner/name and a clone URL, and reads metadata and releases from
// the host's API (GitHub first; Gitea/Forgejo/Codeberg and GitLab use the
// same interface). Everything an API returns is data from outside: it is
// validated before it reaches a path (safepath) and never interpreted.
//
// Tests run against local httptest servers; there is no network access in
// unit tests.
package sources

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// Provider kinds.
const (
	KindGitHub  = "github"
	KindGitLab  = "gitlab"
	KindGitea   = "gitea" // also Forgejo and Codeberg
	KindGeneric = "git"
)

// ErrBadURL is returned for input that is not a repository link.
var ErrBadURL = errors.New("not a repository link")

// Parsed is a recognised repository link.
type Parsed struct {
	Source   repometa.Source // URL is the canonical clone URL
	Provider string
	// WebURL is the page of the repository, if the host has one.
	WebURL string
}

// scp-like ssh syntax: git@github.com:owner/name.git
var scpLike = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):(.+)$`)

// knownHosts maps well-known hosts to their API kind.
var knownHosts = map[string]string{
	"github.com":   KindGitHub,
	"gitlab.com":   KindGitLab,
	"codeberg.org": KindGitea,
	"gitea.com":    KindGitea,
}

// ParseURL recognises a repository link. Accepted: https/http URLs (also
// with /tree/… or GitLab's /-/… suffix), "github.com/owner/name", scp-like
// "git@host:owner/name.git" and ssh:// or git:// URLs.
func ParseURL(input string) (Parsed, error) {
	in := strings.TrimSpace(input)
	if in == "" {
		return Parsed{}, ErrBadURL
	}
	var u *url.URL
	if m := scpLike.FindStringSubmatch(in); m != nil && !strings.Contains(in, "://") {
		u = &url.URL{Scheme: "ssh", User: url.User(m[1]), Host: m[2], Path: "/" + m[3]}
	} else {
		if !strings.Contains(in, "://") {
			in = "https://" + in
		}
		var err error
		u, err = url.Parse(in)
		if err != nil {
			return Parsed{}, fmt.Errorf("%w: %v", ErrBadURL, err)
		}
	}
	switch u.Scheme {
	case "https", "http", "ssh", "git":
	default:
		return Parsed{}, fmt.Errorf("%w: scheme %q", ErrBadURL, u.Scheme)
	}
	if u.Host == "" {
		return Parsed{}, fmt.Errorf("%w: no host", ErrBadURL)
	}
	if u.User != nil && u.Scheme != "ssh" {
		// Never keep credentials from a pasted link (AGENTS.md security).
		u.User = nil
	}
	host := strings.ToLower(u.Host)
	kind := knownHosts[strings.TrimPrefix(host, "www.")]
	host = strings.TrimPrefix(host, "www.")

	path := strings.Trim(u.Path, "/")
	if i := strings.Index(path, "/-/"); i >= 0 { // GitLab UI paths
		path = path[:i]
	}
	segs := strings.Split(path, "/")
	if kind == KindGitHub || kind == KindGitea {
		// Only owner/name; anything after is a page (tree, issues, …).
		if len(segs) > 2 {
			segs = segs[:2]
		}
	}
	if len(segs) < 2 || segs[0] == "" {
		return Parsed{}, fmt.Errorf("%w: owner and name missing", ErrBadURL)
	}
	name := strings.TrimSuffix(segs[len(segs)-1], ".git")
	owner := strings.Join(segs[:len(segs)-1], "/")
	if kind == "" {
		kind = KindGeneric
	}

	clone := url.URL{Scheme: u.Scheme, User: u.User, Host: u.Host, Path: "/" + owner + "/" + name}
	if u.Scheme == "https" || u.Scheme == "http" {
		clone.Host = host
		if kind == KindGitHub || kind == KindGitLab || kind == KindGitea {
			clone.Path += ".git"
		}
	}
	p := Parsed{
		Source:   repometa.Source{URL: clone.String(), Host: host, Owner: owner, Name: name},
		Provider: kind,
	}
	if u.Scheme == "https" || u.Scheme == "http" {
		p.WebURL = u.Scheme + "://" + host + "/" + owner + "/" + name
	} else if kind != KindGeneric {
		p.WebURL = "https://" + u.Hostname() + "/" + owner + "/" + name
	}
	if u.Scheme == "ssh" || u.Scheme == "git" {
		// Folder and identity use the web host, not the ssh port.
		p.Source.Host = strings.ToLower(u.Hostname())
	}

	r := repometa.Repo{Source: p.Source}
	if err := checkSource(r.Source); err != nil {
		return Parsed{}, err
	}
	return p, nil
}

// checkSource applies the schema's rules to host/owner/name so a link with
// unsafe names is refused before anything is written.
func checkSource(s repometa.Source) error {
	r := repometa.Defaults(repometa.KindMirror)
	r.ID = "00000000000000000000000000"
	r.Source = s
	r.Sync = repometa.Sync{Mode: repometa.SyncManual}
	r.Created = r.Created.AddDate(2000, 0, 0)
	var fe *repometa.FieldError
	if err := r.Validate(); err != nil {
		for _, e := range unwrapAll(err) {
			if errors.As(e, &fe) && strings.HasPrefix(fe.Field, "source.") {
				return fmt.Errorf("%w: %s %s", ErrBadURL, fe.Field, fe.Problem)
			}
		}
	}
	return nil
}

func unwrapAll(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
