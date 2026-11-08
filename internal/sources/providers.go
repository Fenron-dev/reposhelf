// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// repoPath escapes owner (possibly nested) and name for API paths.
func repoPath(src repometa.Source) string {
	var segs []string
	for _, s := range strings.Split(src.Owner, "/") {
		segs = append(segs, url.PathEscape(s))
	}
	return strings.Join(segs, "/") + "/" + url.PathEscape(src.Name)
}

// spdx normalises "no license recognised" to "".
func spdx(id string) string {
	if id == "NOASSERTION" {
		return ""
	}
	return id
}

// ---- GitHub (REST v3) ----

type github struct {
	c    *client
	host string
}

type ghRepo struct {
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Topics      []string `json:"topics"`
	License     *struct {
		SPDX string `json:"spdx_id"`
	} `json:"license"`
	Language      string    `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	Archived      bool      `json:"archived"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	HasWiki       bool      `json:"has_wiki"`
	HasIssues     bool      `json:"has_issues"`
	Stars         int       `json:"stargazers_count"`
	CreatedAt     time.Time `json:"created_at"`
	PushedAt      time.Time `json:"pushed_at"`
	HTMLURL       string    `json:"html_url"`
	Parent        *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
}

func (g *github) Metadata(ctx context.Context, src repometa.Source) (Metadata, error) {
	var r ghRepo
	if _, err := g.c.get(ctx, "/repos/"+repoPath(src), &r); err != nil {
		return Metadata{}, err
	}
	m := Metadata{
		Description: r.Description, Homepage: r.Homepage, Topics: r.Topics, Language: r.Language,
		DefaultBranch: r.DefaultBranch, Archived: r.Archived, Private: r.Private, Fork: r.Fork,
		HasWiki: r.HasWiki, HasIssues: r.HasIssues, Stars: r.Stars,
		CreatedAt: ptrTime(r.CreatedAt), PushedAt: ptrTime(r.PushedAt),
	}
	if r.License != nil {
		m.License = spdx(r.License.SPDX)
	}
	if r.Parent != nil && r.Parent.FullName != "" {
		m.ForkParent = g.host + "/" + r.Parent.FullName
	}
	var langs map[string]int64
	if _, err := g.c.get(ctx, "/repos/"+repoPath(src)+"/languages", &langs); err == nil {
		m.Languages = langs
	}
	return m, nil
}

type ghRelease struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
		Browser     string `json:"browser_download_url"`
		API         string `json:"url"`
		Digest      string `json:"digest"`
	} `json:"assets"`
}

func (g *github) Releases(ctx context.Context, src repometa.Source) ([]Release, error) {
	raw, err := getAll[ghRelease](ctx, g.c, "/repos/"+repoPath(src)+"/releases?per_page=100")
	if err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		rel := Release{Tag: r.Tag, Name: r.Name, Body: r.Body, Draft: r.Draft, Prerelease: r.Prerelease, Published: ptrTime(r.Published)}
		for _, a := range r.Assets {
			u := a.Browser
			if g.c.token != "" && a.API != "" {
				// Works for private repositories, unlike the browser URL.
				u = a.API
			}
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, Size: a.Size, ContentType: a.ContentType, URL: u, Digest: a.Digest})
		}
		out = append(out, rel)
	}
	return out, nil
}

func (g *github) Download(ctx context.Context, a Asset, w io.Writer) error {
	accept := ""
	if strings.Contains(a.URL, "/releases/assets/") {
		accept = "application/octet-stream"
	}
	return g.c.download(ctx, a.URL, accept, w)
}

type ghIssue struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	Comments  int        `json:"comments"`
	HTMLURL   string     `json:"html_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest *struct{} `json:"pull_request"`
}

type ghComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

func (g *github) Issues(ctx context.Context, src repometa.Source, since *time.Time) ([]Issue, error) {
	path := "/repos/" + repoPath(src) + "/issues?state=all&per_page=100"
	if since != nil {
		path += "&since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	raw, err := getAll[ghIssue](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		is := Issue{Number: r.Number, Title: r.Title, State: r.State, Author: r.User.Login, Body: r.Body,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ClosedAt: r.ClosedAt, URL: r.HTMLURL}
		for _, l := range r.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		if r.Comments > 0 {
			cs, err := getAll[ghComment](ctx, g.c, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", repoPath(src), r.Number))
			if err != nil {
				return nil, err
			}
			for _, c := range cs {
				is.Comments = append(is.Comments, Comment{Author: c.User.Login, Body: c.Body, CreatedAt: c.CreatedAt})
			}
		}
		out = append(out, is)
	}
	return out, nil
}

func (g *github) ListOwn(ctx context.Context, starred bool) ([]string, error) {
	path := "/user/repos?per_page=100&affiliation=owner"
	if starred {
		path = "/user/starred?per_page=100"
	}
	raw, err := getAll[ghRepo](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range raw {
		out = append(out, r.HTMLURL)
	}
	return out, nil
}

// ---- Gitea / Forgejo / Codeberg (API v1, GitHub-like) ----

type gitea struct {
	c    *client
	host string
}

type gtRepo struct {
	Description   string    `json:"description"`
	Website       string    `json:"website"`
	Topics        []string  `json:"topics"`
	Language      string    `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	Archived      bool      `json:"archived"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	HasWiki       bool      `json:"has_wiki"`
	HasIssues     bool      `json:"has_issues"`
	Stars         int       `json:"stars_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	HTMLURL       string    `json:"html_url"`
	Parent        *struct {
		FullName string `json:"full_name"`
	} `json:"parent"`
	Licenses []string `json:"licenses"`
}

func (g *gitea) Metadata(ctx context.Context, src repometa.Source) (Metadata, error) {
	var r gtRepo
	if _, err := g.c.get(ctx, "/repos/"+repoPath(src), &r); err != nil {
		return Metadata{}, err
	}
	m := Metadata{
		Description: r.Description, Homepage: r.Website, Topics: r.Topics, Language: r.Language,
		DefaultBranch: r.DefaultBranch, Archived: r.Archived, Private: r.Private, Fork: r.Fork,
		HasWiki: r.HasWiki, HasIssues: r.HasIssues, Stars: r.Stars,
		CreatedAt: ptrTime(r.CreatedAt), PushedAt: ptrTime(r.UpdatedAt),
	}
	if len(r.Licenses) > 0 {
		m.License = r.Licenses[0]
	}
	if r.Parent != nil && r.Parent.FullName != "" {
		m.ForkParent = g.host + "/" + r.Parent.FullName
	}
	return m, nil
}

type gtRelease struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (g *gitea) Releases(ctx context.Context, src repometa.Source) ([]Release, error) {
	raw, err := getAll[gtRelease](ctx, g.c, "/repos/"+repoPath(src)+"/releases?limit=50")
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range raw {
		rel := Release{Tag: r.Tag, Name: r.Name, Body: r.Body, Draft: r.Draft, Prerelease: r.Prerelease, Published: ptrTime(r.Published)}
		for _, a := range r.Assets {
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, Size: a.Size, URL: a.URL})
		}
		out = append(out, rel)
	}
	return out, nil
}

func (g *gitea) Download(ctx context.Context, a Asset, w io.Writer) error {
	return g.c.download(ctx, a.URL, "", w)
}

type gtIssue struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	Comments  int        `json:"comments"`
	HTMLURL   string     `json:"html_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	PullRequest *struct{} `json:"pull_request"`
}

func (g *gitea) Issues(ctx context.Context, src repometa.Source, since *time.Time) ([]Issue, error) {
	path := "/repos/" + repoPath(src) + "/issues?state=all&type=issues&limit=50"
	if since != nil {
		path += "&since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	raw, err := getAll[gtIssue](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		is := Issue{Number: r.Number, Title: r.Title, State: r.State, Author: r.User.Login, Body: r.Body,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ClosedAt: r.ClosedAt, URL: r.HTMLURL}
		for _, l := range r.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		if r.Comments > 0 {
			cs, err := getAll[ghComment](ctx, g.c, fmt.Sprintf("/repos/%s/issues/%d/comments", repoPath(src), r.Number))
			if err != nil {
				return nil, err
			}
			for _, c := range cs {
				is.Comments = append(is.Comments, Comment{Author: c.User.Login, Body: c.Body, CreatedAt: c.CreatedAt})
			}
		}
		out = append(out, is)
	}
	return out, nil
}

func (g *gitea) ListOwn(ctx context.Context, starred bool) ([]string, error) {
	path := "/user/repos?limit=50"
	if starred {
		path = "/user/starred?limit=50"
	}
	raw, err := getAll[gtRepo](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range raw {
		out = append(out, r.HTMLURL)
	}
	return out, nil
}

// ---- GitLab (API v4) ----

type gitlab struct {
	c    *client
	host string
}

type glProject struct {
	Description   string    `json:"description"`
	Topics        []string  `json:"topics"`
	DefaultBranch string    `json:"default_branch"`
	Archived      bool      `json:"archived"`
	Visibility    string    `json:"visibility"`
	Stars         int       `json:"star_count"`
	CreatedAt     time.Time `json:"created_at"`
	LastActivity  time.Time `json:"last_activity_at"`
	WebURL        string    `json:"web_url"`
	WikiEnabled   bool      `json:"wiki_enabled"`
	IssuesEnabled bool      `json:"issues_enabled"`
	License       *struct {
		Key string `json:"key"`
	} `json:"license"`
	ForkedFrom *struct {
		PathWithNamespace string `json:"path_with_namespace"`
	} `json:"forked_from_project"`
}

func (g *gitlab) project(src repometa.Source) string {
	return "/projects/" + url.PathEscape(src.Owner+"/"+src.Name)
}

func (g *gitlab) Metadata(ctx context.Context, src repometa.Source) (Metadata, error) {
	var p glProject
	if _, err := g.c.get(ctx, g.project(src)+"?license=true", &p); err != nil {
		return Metadata{}, err
	}
	m := Metadata{
		Description: p.Description, Topics: p.Topics, DefaultBranch: p.DefaultBranch, Archived: p.Archived,
		Private: p.Visibility == "private", HasWiki: p.WikiEnabled, HasIssues: p.IssuesEnabled, Stars: p.Stars,
		CreatedAt: ptrTime(p.CreatedAt), PushedAt: ptrTime(p.LastActivity),
	}
	if p.License != nil {
		m.License = strings.ToUpper(p.License.Key)
	}
	if p.ForkedFrom != nil {
		m.Fork = true
		m.ForkParent = g.host + "/" + p.ForkedFrom.PathWithNamespace
	}
	var langs map[string]float64
	if _, err := g.c.get(ctx, g.project(src)+"/languages", &langs); err == nil {
		best := 0.0
		for l, share := range langs {
			if share > best {
				best, m.Language = share, l
			}
		}
	}
	return m, nil
}

type glRelease struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ReleasedAt  time.Time `json:"released_at"`
	Upcoming    bool      `json:"upcoming_release"`
	Assets      struct {
		Links []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"links"`
	} `json:"assets"`
}

func (g *gitlab) Releases(ctx context.Context, src repometa.Source) ([]Release, error) {
	raw, err := getAll[glRelease](ctx, g.c, g.project(src)+"/releases?per_page=100")
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range raw {
		rel := Release{Tag: r.Tag, Name: r.Name, Body: r.Description, Prerelease: r.Upcoming, Published: ptrTime(r.ReleasedAt)}
		for _, l := range r.Assets.Links {
			rel.Assets = append(rel.Assets, Asset{Name: l.Name, URL: l.URL})
		}
		out = append(out, rel)
	}
	return out, nil
}

func (g *gitlab) Download(ctx context.Context, a Asset, w io.Writer) error {
	return g.c.download(ctx, a.URL, "", w)
}

type glIssue struct {
	IID         int        `json:"iid"`
	Title       string     `json:"title"`
	State       string     `json:"state"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Labels      []string   `json:"labels"`
	WebURL      string     `json:"web_url"`
	Author      struct {
		Username string `json:"username"`
	} `json:"author"`
}

func (g *gitlab) Issues(ctx context.Context, src repometa.Source, since *time.Time) ([]Issue, error) {
	path := g.project(src) + "/issues?per_page=100"
	if since != nil {
		path += "&updated_after=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	}
	raw, err := getAll[glIssue](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []Issue
	for _, r := range raw {
		out = append(out, Issue{Number: r.IID, Title: r.Title, State: r.State, Author: r.Author.Username,
			Labels: r.Labels, Body: r.Description, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			ClosedAt: r.ClosedAt, URL: r.WebURL})
	}
	return out, nil
}

func (g *gitlab) ListOwn(ctx context.Context, starred bool) ([]string, error) {
	path := "/projects?owned=true&per_page=100"
	if starred {
		path = "/projects?starred=true&per_page=100"
	}
	raw, err := getAll[glProject](ctx, g.c, path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range raw {
		out = append(out, p.WebURL)
	}
	return out, nil
}
