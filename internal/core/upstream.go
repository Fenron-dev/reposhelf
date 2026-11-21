// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/atomicfile"
	"github.com/Fenron-dev/reposhelf/internal/layout"
	"github.com/Fenron-dev/reposhelf/internal/sources"
)

// Detected upstream states (KONZEPT 6.2). The user's status_override in
// repo.yaml wins over them (Fero rule: user > detection).
const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
	StatusGone     = "gone"
	StatusArchived = "archived"
	StatusLocal    = "local"   // no upstream
	StatusNew      = "pending" // never synced
)

// Upstream is the content of upstream.json: what RepoShelf learned about
// the original, with a history of earlier metadata (important once the
// original is gone), plus sync state and statistics. It is derived data
// next to repo.yaml and part of the truth for the index.
type Upstream struct {
	Schema       int               `json:"schema"`
	Provider     string            `json:"provider"`
	WebURL       string            `json:"web_url,omitempty"`
	FetchedAt    *time.Time        `json:"fetched_at,omitempty"`
	Metadata     *sources.Metadata `json:"metadata,omitempty"`
	History      []MetaChange      `json:"history,omitempty"`
	Status       string            `json:"status"`
	FailingSince *time.Time        `json:"failing_since,omitempty"`
	ErrorKind    string            `json:"error_kind,omitempty"`
	HostDown     bool              `json:"host_down,omitempty"`
	Stats        Stats             `json:"stats"`
	// DeletedUpstream lists refs that the original deleted; they stay here.
	DeletedUpstream []string   `json:"deleted_upstream,omitempty"`
	LastBackup      string     `json:"last_backup,omitempty"`
	LastSnapshot    *time.Time `json:"last_snapshot,omitempty"`
	IssuesSynced    *time.Time `json:"issues_synced,omitempty"`
	// Forwarded and ForwardError describe the last push passed on to the
	// original (D-029).
	Forwarded    *time.Time `json:"forwarded,omitempty"`
	ForwardError string     `json:"forward_error,omitempty"`
	// Models are Hugging Face model IDs the code refers to (T-9.11).
	Models []string `json:"models,omitempty"`
	// Import is a local folder taken over, checked after the first sync.
	Import *ImportState `json:"import,omitempty"`
	// ExportNote explains why no model folder for programs exists.
	ExportNote string `json:"export_note,omitempty"`
	// AddedBy is "submodule" for repositories taken over as a submodule
	// of another one (their own submodules are not followed, R-016).
	AddedBy string `json:"added_by,omitempty"`
}

// MetaChange keeps metadata as it was before a change upstream.
type MetaChange struct {
	ChangedAt time.Time        `json:"changed_at"`
	Previous  sources.Metadata `json:"previous"`
}

// Stats are measured on the local copy after each sync.
type Stats struct {
	SizeBytes  int64      `json:"size_bytes"`
	Branches   int        `json:"branches"`
	Versions   int        `json:"versions"`
	Releases   int        `json:"releases"`
	LastCommit *time.Time `json:"last_commit,omitempty"`
	Readme     string     `json:"readme,omitempty"`
}

const upstreamSchema = 1

func readUpstream(metaDir string) (Upstream, error) {
	u := Upstream{Schema: upstreamSchema, Status: StatusNew}
	b, err := os.ReadFile(filepath.Join(metaDir, layout.UpstreamFile))
	if errors.Is(err, fs.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return u, fmt.Errorf("%s: %w", layout.UpstreamFile, err)
	}
	return u, nil
}

func writeUpstream(metaDir string, u Upstream) error {
	u.Schema = upstreamSchema
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(metaDir, layout.UpstreamFile), append(b, '\n'), filePerm)
}

// setMetadata stores m and records the previous version in the history if
// something meaningful changed (stars and push dates change constantly and
// are not history).
func (u *Upstream) setMetadata(m sources.Metadata, now time.Time) {
	if u.Metadata != nil && !sameMeta(*u.Metadata, m) {
		u.History = append(u.History, MetaChange{ChangedAt: now, Previous: *u.Metadata})
		const maxHistory = 50
		if len(u.History) > maxHistory {
			u.History = u.History[len(u.History)-maxHistory:]
		}
	}
	u.Metadata = &m
	u.FetchedAt = &now
}

func sameMeta(a, b sources.Metadata) bool {
	a.Stars, b.Stars = 0, 0
	a.PushedAt, b.PushedAt = nil, nil
	a.Languages, b.Languages = nil, nil
	a.Topics, b.Topics = slices.Clone(a.Topics), slices.Clone(b.Topics)
	slices.Sort(a.Topics)
	slices.Sort(b.Topics)
	return reflect.DeepEqual(a, b)
}

const filePerm = 0o644
