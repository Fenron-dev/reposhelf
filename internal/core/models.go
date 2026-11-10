// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// ---- Models a project needs at runtime (T-9.11) ----
//
// Many AI tools download models from the Hugging Face Hub when they start.
// After a sync, the code is searched for the usual calls; the model IDs
// found are listed on the repository page with a button to keep them, and
// become relation suggestions once the model is in the library.

// modelHints finds lines that may name a model (one git grep per sync).
const modelHints = `from_pretrained|hf_hub_download|snapshot_download|repo_id|huggingface\.co/`

var (
	quotedID = regexp.MustCompile(`["']([A-Za-z0-9][A-Za-z0-9_.-]{0,95}/[A-Za-z0-9][A-Za-z0-9_.-]{0,95})["']`)
	hubURL   = regexp.MustCompile(`huggingface\.co/([A-Za-z0-9][A-Za-z0-9_.-]{0,95}/[A-Za-z0-9][A-Za-z0-9_.-]{0,95})`)
	// fileLike filters paths such as "models/voice.onnx" or "data/x.json".
	fileLike = regexp.MustCompile(`(?i)\.(py|json|ya?ml|txt|md|bin|pt|pth|onnx|safetensors|ckpt|gguf|wav|mp3|png|jpe?g|csv|zip|tar|gz)$`)
)

// maxModels bounds the list per repository.
const maxModels = 20

// modelIDs extracts Hugging Face model IDs from code lines.
func modelIDs(lines []string) []string {
	var out []string
	add := func(id string) {
		id = strings.TrimSuffix(id, ".git")
		owner, name, _ := strings.Cut(id, "/")
		if fileLike.MatchString(id) || owner == "datasets" || owner == "spaces" || name == "" ||
			strings.HasPrefix(owner, ".") || slices.Contains(out, id) || len(out) >= maxModels {
			return
		}
		out = append(out, id)
	}
	for _, l := range lines {
		for _, m := range hubURL.FindAllStringSubmatch(l, -1) {
			if !strings.Contains(m[0], "/datasets/") && !strings.Contains(m[0], "/spaces/") {
				add(m[1])
			}
		}
		if strings.Contains(l, "huggingface.co/") {
			continue
		}
		for _, m := range quotedID.FindAllStringSubmatch(l, -1) {
			add(m[1])
		}
	}
	slices.Sort(out)
	return out
}

// detectModels updates the models a repository's code refers to.
func (l *Library) detectModels(ctx context.Context, r *Repo) {
	if r.R().Source.Host == sources.HuggingFaceHost || !l.Git.HasCommits(ctx, r.Paths.Mirror) {
		return
	}
	hits, err := l.Git.Grep(ctx, r.Paths.Mirror, "HEAD", modelHints, false, false, 20)
	if err != nil {
		return
	}
	var lines []string
	for _, h := range hits {
		lines = append(lines, h.Text)
	}
	r.Upstream.Models = modelIDs(lines)
	for _, id := range r.Upstream.Models {
		if e, err := l.Store.ByFolder(ctx, sources.HuggingFaceHost+"/"+id); err == nil {
			addSuggestion(r.R(), "depends_on", e.Key, "code")
		}
	}
}

// ModelRef is a model a repository needs, and whether it is kept.
type ModelRef struct {
	ID     string // owner/name on the Hugging Face Hub
	RepoID string // set when the model is in the library
	URL    string
}

// ModelsOf lists the models found in a repository's code.
func (l *Library) ModelsOf(ctx context.Context, r *Repo) []ModelRef {
	var out []ModelRef
	for _, id := range r.Upstream.Models {
		m := ModelRef{ID: id, URL: "https://" + sources.HuggingFaceHost + "/" + id}
		if e, err := l.Store.ByFolder(ctx, sources.HuggingFaceHost+"/"+id); err == nil {
			m.RepoID = e.ID
		}
		out = append(out, m)
	}
	return out
}
