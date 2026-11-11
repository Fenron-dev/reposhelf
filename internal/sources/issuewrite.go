// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// IssueWriter changes issues on the host (D-031). GitHub and
// Gitea/Forgejo/Codeberg use the same endpoints.
type IssueWriter interface {
	CreateIssue(ctx context.Context, src repometa.Source, title, body string, labels []string) (int, error)
	CommentIssue(ctx context.Context, src repometa.Source, number int, body string) error
	SetIssueState(ctx context.Context, src repometa.Source, number int, state string) error
}

// Writer returns the issue writer of host, or ErrUnsupported.
func (r *Registry) Writer(host string) (IssueWriter, error) {
	a, err := r.API(host)
	if err != nil {
		return nil, err
	}
	if w, ok := a.(IssueWriter); ok {
		return w, nil
	}
	return nil, ErrUnsupported
}

// send makes a JSON request that changes something on the host.
func (c *client) send(ctx context.Context, method, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RepoShelf")
	if c.token != "" {
		req.Header.Set("Authorization", c.authHeader+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s %s: %w", method, path, ErrAuth)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return fmt.Errorf("%s %s: %w", method, path, ErrGone)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%s %s: HTTP %d: %w", method, path, resp.StatusCode, ErrUnavailable)
	case resp.StatusCode >= 300:
		return fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type issueAPI struct{ c *client }

func (a issueAPI) CreateIssue(ctx context.Context, src repometa.Source, title, body string, labels []string) (int, error) {
	in := map[string]any{"title": title, "body": body}
	var out struct {
		Number int `json:"number"`
	}
	if err := a.c.send(ctx, http.MethodPost, "/repos/"+repoPath(src)+"/issues", in, &out); err != nil {
		return 0, err
	}
	if len(labels) > 0 {
		// Labels that do not exist on the host would fail the whole issue;
		// they are set separately and a failure there is ignored.
		_ = a.c.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repoPath(src), out.Number),
			map[string]any{"labels": labels}, nil)
	}
	return out.Number, nil
}

func (a issueAPI) CommentIssue(ctx context.Context, src repometa.Source, number int, body string) error {
	return a.c.send(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repoPath(src), number),
		map[string]any{"body": body}, nil)
}

func (a issueAPI) SetIssueState(ctx context.Context, src repometa.Source, number int, state string) error {
	return a.c.send(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/%d", repoPath(src), number),
		map[string]any{"state": state}, nil)
}

func (g *github) CreateIssue(ctx context.Context, src repometa.Source, title, body string, labels []string) (int, error) {
	return issueAPI{g.c}.CreateIssue(ctx, src, title, body, labels)
}
func (g *github) CommentIssue(ctx context.Context, src repometa.Source, n int, body string) error {
	return issueAPI{g.c}.CommentIssue(ctx, src, n, body)
}
func (g *github) SetIssueState(ctx context.Context, src repometa.Source, n int, state string) error {
	return issueAPI{g.c}.SetIssueState(ctx, src, n, state)
}

func (g *gitea) CreateIssue(ctx context.Context, src repometa.Source, title, body string, labels []string) (int, error) {
	// Gitea expects label IDs, not names: create without labels.
	return issueAPI{g.c}.CreateIssue(ctx, src, title, body, nil)
}
func (g *gitea) CommentIssue(ctx context.Context, src repometa.Source, n int, body string) error {
	return issueAPI{g.c}.CommentIssue(ctx, src, n, body)
}
func (g *gitea) SetIssueState(ctx context.Context, src repometa.Source, n int, state string) error {
	return issueAPI{g.c}.SetIssueState(ctx, src, n, state)
}
