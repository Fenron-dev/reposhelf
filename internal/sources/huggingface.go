// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/repometa"
)

// KindHuggingFace is the Hugging Face Hub: models are Git repositories
// with large files in LFS (T-9.11). Datasets and Spaces follow later.
const KindHuggingFace = "huggingface"

// HuggingFaceHost is the host of the Hugging Face Hub.
const HuggingFaceHost = "huggingface.co"

type huggingface struct {
	c    *client
	host string
}

type hfModel struct {
	ID           string    `json:"id"`
	Private      bool      `json:"private"`
	Gated        any       `json:"gated"` // false, "auto" or "manual"
	Tags         []string  `json:"tags"`
	PipelineTag  string    `json:"pipeline_tag"`
	LibraryName  string    `json:"library_name"`
	LastModified time.Time `json:"lastModified"`
	CreatedAt    time.Time `json:"createdAt"`
	Likes        int       `json:"likes"`
	CardData     struct {
		License any `json:"license"` // string or list
	} `json:"cardData"`
}

type hfEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		Size int64 `json:"size"`
	} `json:"lfs"`
}

func (h *huggingface) Metadata(ctx context.Context, src repometa.Source) (Metadata, error) {
	var m hfModel
	if _, err := h.c.get(ctx, "/models/"+repoPath(src), &m); err != nil {
		return Metadata{}, err
	}
	desc := []string{"Modell"}
	for _, s := range []string{m.PipelineTag, m.LibraryName} {
		if s != "" {
			desc = append(desc, s)
		}
	}
	if g, ok := m.Gated.(string); ok && g != "" {
		desc = append(desc, "Zugang nur nach Zustimmung auf Hugging Face")
	}
	md := Metadata{
		Description: strings.Join(desc, " · "), Topics: hfTopics(m.Tags), DefaultBranch: "main",
		Private: m.Private, Stars: m.Likes, CreatedAt: ptrTime(m.CreatedAt), PushedAt: ptrTime(m.LastModified),
		License: hfLicense(m),
	}
	if size, err := h.size(ctx, src); err == nil {
		md.Size = size
	}
	return md, nil
}

// size adds up all files of the main revision; LFS files count with
// their real size.
func (h *huggingface) size(ctx context.Context, src repometa.Source) (int64, error) {
	entries, err := getAll[hfEntry](ctx, h.c, "/models/"+repoPath(src)+"/tree/main?recursive=true")
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		if e.LFS != nil {
			total += e.LFS.Size
		} else {
			total += e.Size
		}
	}
	return total, nil
}

func hfLicense(m hfModel) string {
	switch l := m.CardData.License.(type) {
	case string:
		return l
	case []any:
		if len(l) > 0 {
			if s, ok := l[0].(string); ok {
				return s
			}
		}
	}
	for _, t := range m.Tags {
		if s, ok := strings.CutPrefix(t, "license:"); ok {
			return s
		}
	}
	return ""
}

// hfTopics keeps plain tags (no "license:", "region:" … prefixes).
func hfTopics(tags []string) []string {
	var out []string
	for _, t := range tags {
		if !strings.Contains(t, ":") && len(out) < 10 {
			out = append(out, strings.ToLower(t))
		}
	}
	return out
}

func (h *huggingface) Releases(context.Context, repometa.Source) ([]Release, error) {
	return nil, nil
}

func (h *huggingface) Issues(context.Context, repometa.Source, *time.Time) ([]Issue, error) {
	return nil, ErrUnsupported
}

func (h *huggingface) Download(context.Context, Asset, io.Writer) error { return ErrUnsupported }

func (h *huggingface) ListOwn(context.Context, bool) ([]string, error) { return nil, ErrUnsupported }

func (h *huggingface) Artifacts(context.Context, repometa.Source) ([]WorkflowArtifact, error) {
	return nil, ErrUnsupported
}
