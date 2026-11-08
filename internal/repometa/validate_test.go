// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

func validRepo() Repo {
	r := Defaults(KindMirror)
	r.ID = "01JC8QH3K7Z9X2M4N6P8R0T2V4"
	r.Source = Source{URL: "https://github.com/Fenron-dev/Fero", Host: "github.com", Owner: "Fenron-dev", Name: "Fero"}
	every := Duration(6 * time.Hour)
	r.Sync = Sync{Mode: SyncFollow, Interval: &every}
	r.Created = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	return r
}

func ptrTo[T any](v T) *T { return &v }

func TestValidateAcceptsValid(t *testing.T) {
	cases := map[string]func(r *Repo){
		"base":              func(r *Repo) {},
		"fork":              func(r *Repo) { r.Kind = KindFork; r.ForkOf = ptrTo("github.com/orig/Fero") },
		"fork by ulid":      func(r *Repo) { r.Kind = KindFork; r.ForkOf = ptrTo("01JC8QH3K7Z9X2M4N6P8R0T2V5") },
		"nested owner":      func(r *Repo) { r.Source.Owner = "group/sub" },
		"ssh with user":     func(r *Repo) { r.Source.URL = "ssh://git@github.com/Fenron-dev/Fero.git" },
		"once w/o interval": func(r *Repo) { r.Sync = Sync{Mode: SyncOnce} },
		"hierarchic tag":    func(r *Repo) { r.Tags = []string{"ttrpg/tool", "rust"} },
		"custom relation": func(r *Repo) {
			r.Relations = []Relation{{Type: "inspired_by", Target: "github.com/a/b", State: RelationRejected}}
		},
		"override":  func(r *Repo) { r.StatusOverride = ptrTo(UpstreamArchived) },
		"keep zero": func(r *Repo) { r.Retention.ReleasesKeep = ptrTo(0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRepo()
			mutate(&r)
			if err := r.Validate(); err != nil {
				t.Errorf("Validate() = %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name, field string
		mutate      func(r *Repo)
	}{
		{"schema", "schema", func(r *Repo) { r.Schema = 2 }},
		{"id empty", "id", func(r *Repo) { r.ID = "" }},
		{"id lower case", "id", func(r *Repo) { r.ID = strings.ToLower(r.ID) }},
		{"id with I", "id", func(r *Repo) { r.ID = "01JC8QH3K7Z9X2M4N6P8R0T2VI" }},
		{"id overflow", "id", func(r *Repo) { r.ID = "81JC8QH3K7Z9X2M4N6P8R0T2V4" }},
		{"url missing", "source.url", func(r *Repo) { r.Source.URL = "" }},
		{"url relative", "source.url", func(r *Repo) { r.Source.URL = "Fenron-dev/Fero" }},
		{"url file scheme", "source.url", func(r *Repo) { r.Source.URL = "file:///etc/passwd" }},
		{"url token", "source.url", func(r *Repo) { r.Source.URL = "https://ghp_secret@github.com/a/b" }},
		{"url ssh password", "source.url", func(r *Repo) { r.Source.URL = "ssh://git:pw@github.com/a/b" }},
		{"host dotdot", "source.host", func(r *Repo) { r.Source.Host = ".." }},
		{"owner empty", "source.owner", func(r *Repo) { r.Source.Owner = "" }},
		{"owner traversal", "source.owner", func(r *Repo) { r.Source.Owner = "a/../b" }},
		{"name slash", "source.name", func(r *Repo) { r.Source.Name = "a/b" }},
		{"name control char", "source.name", func(r *Repo) { r.Source.Name = "a\x00b" }},
		{"name spaces", "source.name", func(r *Repo) { r.Source.Name = " Fero" }},
		{"name too long", "source.name", func(r *Repo) { r.Source.Name = strings.Repeat("x", safepath.MaxSegmentBytes+1) }},
		{"name windows device", "source.name", func(r *Repo) { r.Source.Name = "con" }},
		{"name trailing dot", "source.name", func(r *Repo) { r.Source.Name = "Fero." }},
		{"owner windows char", "source.owner", func(r *Repo) { r.Source.Owner = "group/a?b" }},
		{"host with port", "source.host", func(r *Repo) { r.Source.Host = "gitea.local:3000" }},
		{"collection device", "collections[0]", func(r *Repo) { r.Collections = []string{"NUL"} }},
		{"target device", "relations[0].target", func(r *Repo) {
			r.Relations = []Relation{{Type: "related", Target: "github.com/a/aux.git", State: RelationConfirmed}}
		}},
		{"kind", "kind", func(r *Repo) { r.Kind = "copy" }},
		{"fork without origin", "fork_of", func(r *Repo) { r.Kind = KindFork }},
		{"origin without fork", "fork_of", func(r *Repo) { r.ForkOf = ptrTo("github.com/a/b") }},
		{"fork of itself", "fork_of", func(r *Repo) { r.Kind = KindFork; r.ForkOf = ptrTo("github.com/Fenron-dev/Fero") }},
		{"sync mode", "sync.mode", func(r *Repo) { r.Sync.Mode = "" }},
		{"follow w/o interval", "sync.interval", func(r *Repo) { r.Sync.Interval = nil }},
		{"interval too short", "sync.interval", func(r *Repo) { r.Sync.Interval = ptrTo(Duration(time.Minute)) }},
		{"status override", "status_override", func(r *Repo) { r.StatusOverride = ptrTo(UpstreamStatus("fine")) }},
		{"tag wildcard", "tags[0]", func(r *Repo) { r.Tags = []string{"ttrpg/*"} }},
		{"tag empty level", "tags[0]", func(r *Repo) { r.Tags = []string{"ttrpg//tool"} }},
		{"tag duplicate", "tags[1]", func(r *Repo) { r.Tags = []string{"go", "go"} }},
		{"collection slash", "collections[0]", func(r *Repo) { r.Collections = []string{"a/b"} }},
		{"relation type", "relations[0].type", func(r *Repo) {
			r.Relations = []Relation{{Type: "Depends On", Target: "github.com/a/b", State: RelationConfirmed}}
		}},
		{"relation target", "relations[0].target", func(r *Repo) {
			r.Relations = []Relation{{Type: "related", Target: "Fero", State: RelationConfirmed}}
		}},
		{"relation to itself", "relations[0].target", func(r *Repo) {
			r.Relations = []Relation{{Type: "related", Target: r.ID, State: RelationConfirmed}}
		}},
		{"relation state", "relations[0].state", func(r *Repo) {
			r.Relations = []Relation{{Type: "related", Target: "github.com/a/b", State: "maybe"}}
		}},
		{"relation duplicate", "relations[1]", func(r *Repo) {
			rel := Relation{Type: "related", Target: "github.com/a/b", State: RelationConfirmed}
			r.Relations = []Relation{rel, rel}
		}},
		{"releases_keep negative", "retention.releases_keep", func(r *Repo) { r.Retention.ReleasesKeep = ptrTo(-1) }},
		{"pinned empty", "retention.pinned[0]", func(r *Repo) { r.Retention.Pinned = []string{" "} }},
		{"snapshot zero", "retention.snapshots.every", func(r *Repo) { r.Retention.Snapshots.Every = ptrTo(Duration(0)) }},
		{"capsules negative", "buildable.capsules_keep", func(r *Repo) { r.Buildable.CapsulesKeep = ptrTo(-1) }},
		{"spdx blank", "license.spdx", func(r *Repo) { r.License.SPDX = ptrTo(" ") }},
		{"created missing", "created", func(r *Repo) { r.Created = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validRepo()
			tt.mutate(&r)
			err := r.Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tt.field {
				t.Errorf("Validate() = %v, want field %s", err, tt.field)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	r := validRepo()
	r.ID = ""
	r.Kind = "copy"
	err := r.Validate()
	if !strings.Contains(err.Error(), "id:") || !strings.Contains(err.Error(), "kind:") {
		t.Errorf("Validate() = %v, want both id and kind", err)
	}
}
