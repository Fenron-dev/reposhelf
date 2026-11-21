// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// Publisher creates a repository and its releases on a host – used to
// revive an archived repository (V-5, T-9.24). GitHub and Gitea/Forgejo.
type Publisher interface {
	// Login is the user the token belongs to.
	Login(ctx context.Context) (string, error)
	// CreateRepo creates owner/name (owner "" or the login: the user's
	// own account, otherwise an organisation) and returns its clone URL
	// and web page.
	CreateRepo(ctx context.Context, owner, name, description string, private bool) (cloneURL, webURL string, err error)
	// CreateRelease creates a release for an existing tag.
	CreateRelease(ctx context.Context, owner, name string, rel Release) (int64, error)
	// UploadAsset attaches a file to a release.
	UploadAsset(ctx context.Context, owner, name string, releaseID int64, file string, size int64, r io.Reader) error
}

// Publisher returns the publisher for host, or ErrUnsupported.
func (r *Registry) Publisher(host string) (Publisher, error) {
	a, err := r.API(host)
	if err != nil {
		return nil, err
	}
	if p, ok := a.(Publisher); ok {
		return p, nil
	}
	return nil, ErrUnsupported
}

type created struct {
	CloneURL string `json:"clone_url"`
	HTMLURL  string `json:"html_url"`
}

func createRepo(ctx context.Context, c *client, login, owner, name, desc string, private bool) (string, string, error) {
	path := "/user/repos"
	if owner != "" && !strings.EqualFold(owner, login) {
		path = "/orgs/" + url.PathEscape(owner) + "/repos"
	}
	var out created
	in := map[string]any{"name": name, "description": desc, "private": private, "auto_init": false}
	if err := c.send(ctx, http.MethodPost, path, in, &out); err != nil {
		return "", "", err
	}
	if out.CloneURL == "" {
		return "", "", fmt.Errorf("host returned no clone URL")
	}
	return out.CloneURL, out.HTMLURL, nil
}

func createRelease(ctx context.Context, c *client, owner, name string, rel Release) (int64, error) {
	in := map[string]any{"tag_name": rel.Tag, "name": rel.Name, "body": rel.Body, "prerelease": rel.Prerelease, "draft": false}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.send(ctx, http.MethodPost, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/releases", in, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func login(ctx context.Context, c *client, field string) (string, error) {
	var u map[string]any
	if _, err := c.get(ctx, "/user", &u); err != nil {
		return "", err
	}
	s, _ := u[field].(string)
	if s == "" {
		return "", fmt.Errorf("host returned no user name")
	}
	return s, nil
}

// upload sends a body that is not JSON (asset uploads).
func (c *client) upload(ctx context.Context, method, target, contentType string, size int64, body io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RepoShelf")
	if c.token != "" {
		req.Header.Set("Authorization", c.authHeader+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("upload: %w", ErrAuth)
		}
		return fmt.Errorf("upload: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// ---- GitHub ----

func (g *github) Login(ctx context.Context) (string, error) { return login(ctx, g.c, "login") }

func (g *github) CreateRepo(ctx context.Context, owner, name, desc string, private bool) (string, string, error) {
	me, err := g.Login(ctx)
	if err != nil {
		return "", "", err
	}
	return createRepo(ctx, g.c, me, owner, name, desc, private)
}

func (g *github) CreateRelease(ctx context.Context, owner, name string, rel Release) (int64, error) {
	return createRelease(ctx, g.c, owner, name, rel)
}

// UploadAsset uses GitHub's upload host (uploads.github.com, or the API
// base for GitHub Enterprise and tests).
func (g *github) UploadAsset(ctx context.Context, owner, name string, id int64, file string, size int64, r io.Reader) error {
	base := "https://uploads.github.com"
	if !strings.HasPrefix(g.c.base, "https://api.github.com") {
		base = g.c.base
	}
	target := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s", base, url.PathEscape(owner), url.PathEscape(name), id, url.QueryEscape(file))
	return g.c.upload(ctx, http.MethodPost, target, "application/octet-stream", size, r)
}

// ---- Gitea / Forgejo / Codeberg ----

func (g *gitea) Login(ctx context.Context) (string, error) { return login(ctx, g.c, "login") }

func (g *gitea) CreateRepo(ctx context.Context, owner, name, desc string, private bool) (string, string, error) {
	me, err := g.Login(ctx)
	if err != nil {
		return "", "", err
	}
	return createRepo(ctx, g.c, me, owner, name, desc, private)
}

func (g *gitea) CreateRelease(ctx context.Context, owner, name string, rel Release) (int64, error) {
	return createRelease(ctx, g.c, owner, name, rel)
}

// UploadAsset sends the file as multipart form ("attachment").
func (g *gitea) UploadAsset(ctx context.Context, owner, name string, id int64, file string, size int64, r io.Reader) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		part, err := mw.CreateFormFile("attachment", file)
		if err == nil {
			_, err = io.Copy(part, r)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	target := fmt.Sprintf("%s/repos/%s/%s/releases/%d/assets?name=%s", g.c.base, url.PathEscape(owner), url.PathEscape(name), id, url.QueryEscape(file))
	return g.c.upload(ctx, http.MethodPost, target, mw.FormDataContentType(), -1, pr)
}
