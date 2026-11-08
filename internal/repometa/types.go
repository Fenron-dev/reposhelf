// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"strings"
	"time"
)

// CurrentSchema is the repo.yaml schema version this binary reads and writes.
// Raising it requires a migration in migrate.go and a fixture of the old
// version in testdata/ (AGENTS.md).
const CurrentSchema = 1

// Repo is the content of one repo.yaml (docs/SCHEMA.md). Every field has a
// yaml tag: the round-trip merge uses the tags to tell known keys from
// unknown ones. Known fields are always written (no omitempty, except where
// noted) so that hand editors see every available setting.
type Repo struct {
	Schema         int             `yaml:"schema"`
	ID             string          `yaml:"id"`
	Source         Source          `yaml:"source"`
	Kind           Kind            `yaml:"kind"`
	ForkOf         *string         `yaml:"fork_of"`
	Sync           Sync            `yaml:"sync"`
	StatusOverride *UpstreamStatus `yaml:"status_override"`
	Include        Include         `yaml:"include"`
	Tags           []string        `yaml:"tags"`
	Collections    []string        `yaml:"collections"`
	Relations      []Relation      `yaml:"relations"`
	Retention      Retention       `yaml:"retention"`
	Buildable      Buildable       `yaml:"buildable"`
	License        License         `yaml:"license"`
	Created        time.Time       `yaml:"created"`
}

// Source identifies where the repository comes from.
type Source struct {
	URL  string `yaml:"url"`
	Host string `yaml:"host"`
	// Owner may contain "/" for nested namespaces (GitLab groups).
	Owner string `yaml:"owner"`
	Name  string `yaml:"name"`
}

// HostName is the host without port. It names the host folder (D-018).
func (s Source) HostName() string {
	name, _, _ := strings.Cut(s.Host, ":")
	return name
}

// Segments are the folder names below repos/, meta/ or releases/ for this
// repository: host name, each owner level, name. They are only safe to use
// after Validate.
func (s Source) Segments() []string {
	segs := []string{s.HostName()}
	segs = append(segs, strings.Split(s.Owner, "/")...)
	return append(segs, s.Name)
}

// Key identifies the repository by its folder: hostname/owner/name. Two
// repositories with the same key would share one folder.
func (s Source) Key() string { return strings.Join(s.Segments(), "/") }

// Kind is the role of a repository (KONZEPT 3).
type Kind string

// Kinds.
const (
	KindOwn    Kind = "own"
	KindMirror Kind = "mirror"
	KindFork   Kind = "fork"
)

// Sync configures how the repository is kept up to date.
type Sync struct {
	Mode SyncMode `yaml:"mode"`
	// Interval is required for mode follow; null otherwise.
	Interval    *Duration  `yaml:"interval"`
	LastSuccess *time.Time `yaml:"last_success"`
	LastCheck   *time.Time `yaml:"last_check"`
	LastError   *string    `yaml:"last_error"`
}

// SyncMode tells when a repository is fetched.
type SyncMode string

// Sync modes.
const (
	SyncOnce   SyncMode = "once"
	SyncFollow SyncMode = "follow"
	SyncManual SyncMode = "manual"
)

// UpstreamStatus is the state of the original repository (KONZEPT 6.2). The
// detected value lives in the index; repo.yaml only stores a user override,
// which always wins (Fero status ranking).
type UpstreamStatus string

// Upstream states.
const (
	UpstreamOK       UpstreamStatus = "ok"
	UpstreamDegraded UpstreamStatus = "degraded"
	UpstreamGone     UpstreamStatus = "gone"
	UpstreamArchived UpstreamStatus = "archived"
)

// Include selects what is archived besides the Git history.
type Include struct {
	Releases   bool `yaml:"releases"`
	LFS        bool `yaml:"lfs"`
	Submodules bool `yaml:"submodules"`
	Issues     bool `yaml:"issues"`
	Wiki       bool `yaml:"wiki"`
}

// Relation is a directed edge from this repository to another one
// (KONZEPT 6.7). The backlink is derived, never stored on the other side.
type Relation struct {
	Type   string        `yaml:"type"`
	Target string        `yaml:"target"`
	State  RelationState `yaml:"state"`
	// Source tells how the relation was found (manual, readme, dependency,
	// fork, wikilink). Optional, so it is omitted when empty.
	Source string `yaml:"source,omitempty"`
}

// RelationState marks whether a relation was confirmed by the user.
type RelationState string

// Relation states. Rejected suggestions are kept so they are not offered
// again (KONZEPT 6.7).
const (
	RelationConfirmed RelationState = "confirmed"
	RelationSuggested RelationState = "suggested"
	RelationRejected  RelationState = "rejected"
)

// Relation types known today. The list is open (KONZEPT 6.7): other types
// that match the naming rule are accepted.
var KnownRelationTypes = []string{
	"plugin_of", "depends_on", "fork_of", "successor_of",
	"delivers_to", "uses_code_from", "related",
}

// Retention holds per-repo overrides of the retention rules. null means
// "inherit from collection or global rules" (KONZEPT 6.10).
type Retention struct {
	ReleasesKeep     *int             `yaml:"releases_keep"`
	Pinned           []string         `yaml:"pinned"`
	Snapshots        SnapshotRule     `yaml:"snapshots"`
	ForcePushBackups ForcePushBackups `yaml:"force_push_backups"`
}

// SnapshotRule configures periodic ref snapshots.
type SnapshotRule struct {
	Every   *Duration `yaml:"every"`
	KeepFor *Duration `yaml:"keep_for"`
}

// ForcePushBackups configures how long force-push backups are kept.
type ForcePushBackups struct {
	KeepFor *Duration `yaml:"keep_for"`
}

// Buildable configures build capsules (KONZEPT 6.11).
type Buildable struct {
	Enabled bool `yaml:"enabled"`
	// CapsulesKeep: null = inherit.
	CapsulesKeep *int `yaml:"capsules_keep"`
}

// License is the detected license of the repository.
type License struct {
	// SPDX: null = not detected yet or no license (UI shows a warning).
	SPDX    *string `yaml:"spdx"`
	Checked *Date   `yaml:"checked"`
}
