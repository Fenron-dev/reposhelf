// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mustParse(t *testing.T, raw []byte) *Document {
	t.Helper()
	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return d
}

func mustMarshal(t *testing.T, d *Document) []byte {
	t.Helper()
	out, err := d.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return out
}

func assertContains(t *testing.T, out []byte, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !bytes.Contains(out, []byte(w)) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func assertNotContains(t *testing.T, out []byte, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if bytes.Contains(out, []byte(w)) {
			t.Errorf("output still contains %q:\n%s", w, out)
		}
	}
}

// The hand-written schema-1 fixture is the "previous version" test of
// AGENTS.md: it must keep parsing in every later version.
func TestParseSchema1HandFixture(t *testing.T) {
	d := mustParse(t, mustReadFixture(t, "schema1-hand.yaml"))
	r := d.Repo

	if r.Kind != KindOwn || r.Source.Name != "Fero" || r.Sync.Mode != SyncFollow {
		t.Errorf("basic fields wrong: %+v", r)
	}
	if got := time.Duration(*r.Sync.Interval); got != 6*time.Hour {
		t.Errorf("interval = %s, want 6h", got)
	}
	if got := time.Duration(*r.Retention.Snapshots.KeepFor); got != 180*day {
		t.Errorf("snapshots.keep_for = %s, want 180d", got)
	}
	if !r.Include.Issues {
		t.Error("include.issues absent for kind own: want default true")
	}
	if r.Relations[0].State != RelationConfirmed {
		t.Errorf("relation without state: got %q, want confirmed", r.Relations[0].State)
	}
	if r.License.Checked.String() != "2026-11-08" {
		t.Errorf("license.checked = %s", r.License.Checked)
	}
	wantWarn := []string{"source.mirror_hint", "relations[1].weight", "future_section"}
	if len(d.Warnings) != len(wantWarn) {
		t.Fatalf("warnings = %q, want %d", d.Warnings, len(wantWarn))
	}
	for i, w := range wantWarn {
		if !strings.Contains(d.Warnings[i], w) {
			t.Errorf("warning %d = %q, want %q", i, d.Warnings[i], w)
		}
	}
}

func TestParseSchema1MinimalFixture(t *testing.T) {
	d := mustParse(t, mustReadFixture(t, "schema1-minimal.yaml"))
	inc := d.Repo.Include
	if !inc.Releases || !inc.LFS || !inc.Submodules || inc.Issues || inc.Wiki {
		t.Errorf("include defaults for mirror wrong: %+v", inc)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", d.Warnings)
	}
	// Writing fills in every known key so a hand editor sees the options.
	assertContains(t, mustMarshal(t, d), "include:\n  releases: true", "retention:", "fork_of: null")
}

func TestRoundTripKeepsUnknownKeysAndComments(t *testing.T) {
	d := mustParse(t, mustReadFixture(t, "schema1-hand.yaml"))
	out := mustMarshal(t, d)

	assertContains(t, out,
		"# Fixture: a hand-edited", // document head comment
		"# ULID, stable across renames",
		"mirror_hint: fast # unknown key inside a known section",
		"weight: 3",
		"# A section written by a newer RepoShelf.\nfuture_section:\n  answer: 42",
		"tags: [medien/beschaffung, rust, tauri]", // flow style kept
		"checked: 2026-11-08\n",                   // date stays a plain date
	)

	again := mustParse(t, out)
	if !bytes.Equal(mustMarshal(t, again), out) {
		t.Error("Marshal is not idempotent")
	}
	if len(again.Warnings) != len(d.Warnings) {
		t.Errorf("warnings changed after round trip: %q -> %q", d.Warnings, again.Warnings)
	}
}

func TestMarshalAppliesChanges(t *testing.T) {
	d := mustParse(t, mustReadFixture(t, "schema1-hand.yaml"))
	r := &d.Repo

	// Reverse the relations: the unknown key must follow its entry.
	r.Relations[0], r.Relations[1] = r.Relations[1], r.Relations[0]
	r.Relations = append(r.Relations, Relation{Type: "related", Target: "github.com/x/y", State: RelationSuggested, Source: "readme"})
	r.Tags = []string{"rust", "go"}
	r.Retention.ReleasesKeep = nil // back to "inherit"
	errText := "fetch failed"
	r.Sync.LastError = &errText
	out := mustMarshal(t, d)

	got := mustParse(t, out)
	if got.Repo.Relations[0].Type != "successor_of" || len(got.Repo.Relations) != 3 {
		t.Errorf("relations = %+v", got.Repo.Relations)
	}
	if !strings.Contains(got.Warnings[1], "relations[0].weight") {
		t.Errorf("unknown key did not follow its relation: %q", got.Warnings)
	}
	assertContains(t, out,
		"tags: [rust, go]",
		"releases_keep: null",
		"last_error: fetch failed",
		"source: readme",
		"future_section:",
	)
	assertNotContains(t, out, "tauri")

	// Dropping the relation drops its unknown key as well.
	got.Repo.Relations = got.Repo.Relations[1:]
	assertNotContains(t, mustMarshal(t, got), "weight")
}

func TestOmittedKnownKeyIsRemoved(t *testing.T) {
	d := mustParse(t, mustReadFixture(t, "schema1-hand.yaml"))
	d.Repo.Relations[1].Source = "manual"
	out := mustMarshal(t, d)
	assertContains(t, out, "source: manual")

	again := mustParse(t, out)
	again.Repo.Relations[1].Source = "" // omitempty: key disappears
	assertNotContains(t, mustMarshal(t, again), "source: manual")
}

func TestNewDocumentRoundTrip(t *testing.T) {
	first := mustMarshal(t, New(validRepo()))
	second := mustMarshal(t, mustParse(t, first))
	if !bytes.Equal(first, second) {
		t.Errorf("new document changed on round trip:\n%s\n---\n%s", first, second)
	}
}

func TestParseRejects(t *testing.T) {
	valid := string(mustReadFixture(t, "schema1-minimal.yaml"))
	tests := []struct {
		name    string
		raw     string
		wantErr error // nil = any error
	}{
		{"empty file", "", ErrInvalid},
		{"top level list", "- a\n", ErrInvalid},
		{"no schema", strings.Replace(valid, "schema: 1\n", "", 1), ErrNoSchema},
		{"null schema", strings.Replace(valid, "schema: 1", "schema: null", 1), ErrNoSchema},
		{"schema zero", strings.Replace(valid, "schema: 1", "schema: 0", 1), ErrInvalid},
		{"schema text", strings.Replace(valid, "schema: 1", "schema: eins", 1), ErrInvalid},
		{"newer schema", strings.Replace(valid, "schema: 1", "schema: 2", 1), ErrNewerSchema},
		{"broken yaml", "schema: [1\n", nil},
		{"wrong type", valid + "tags: tauri\n", nil},
		{"bad duration", strings.Replace(valid, "mode: manual", "mode: follow\n  interval: 1.5h", 1), nil},
		{"invalid field", strings.Replace(valid, "kind: mirror", "kind: copy", 1), ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.raw))
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, mustReadFixture(t, "schema1-minimal.yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("Load() error = %v", err)
	}

	big := filepath.Join(dir, "big.yaml")
	if err := os.WriteFile(big, bytes.Repeat([]byte("#"), maxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Load(big) err = %v, want ErrTooLarge", err)
	}

	if _, err := Load(filepath.Join(dir, "missing.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load(missing) err = %v, want ErrNotExist", err)
	}
}

// The framework must run registered steps in order and bump the number.
func TestMigrate(t *testing.T) {
	t.Run("missing step", func(t *testing.T) {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte("schema: 0\n"), &root); err != nil {
			t.Fatal(err)
		}
		if err := migrate(root.Content[0], 0); err == nil {
			t.Error("migrate() without step: error = nil")
		}
	})
	t.Run("step runs and bumps schema", func(t *testing.T) {
		saved := migrations
		t.Cleanup(func() { migrations = saved })
		migrations = map[int]migration{0: func(root *yaml.Node) error {
			// Example of a schema change: a renamed key.
			for i := 0; i < len(root.Content); i += 2 {
				if root.Content[i].Value == "old_name" {
					root.Content[i].Value = "new_name"
				}
			}
			return nil
		}}

		var doc yaml.Node
		if err := yaml.Unmarshal([]byte("schema: 0\nold_name: x # kept\n"), &doc); err != nil {
			t.Fatal(err)
		}
		root := doc.Content[0]
		if err := migrate(root, 0); err != nil {
			t.Fatal(err)
		}
		out, err := yaml.Marshal(&doc)
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, out, "schema: 1", "new_name: x # kept")
	})
}

// The example in docs/SCHEMA.md must stay valid, so the binding document
// cannot drift away from the code unnoticed.
func TestSchemaDocExample(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "SCHEMA.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(doc), "## Beispiel\n\n```yaml\n")
	if !ok {
		t.Fatal("docs/SCHEMA.md: example block not found")
	}
	example, _, _ := strings.Cut(rest, "```")
	d := mustParse(t, []byte(example))
	if len(d.Warnings) != 0 {
		t.Errorf("example has unknown keys: %q", d.Warnings)
	}
	if out := mustMarshal(t, d); string(out) != example {
		t.Errorf("example is not in canonical form:\n%s", out)
	}
}
