// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T, opts Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func entry(id, owner, name string) Entry {
	return Entry{
		ID: id, Key: "github.com/" + owner + "/" + name, Host: "github.com", Owner: owner, Name: name,
		URL: "https://github.com/" + owner + "/" + name, Kind: "mirror", SyncMode: "follow",
		Interval: 6 * time.Hour, Status: "ok", Created: time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC),
		MetaPath: "/x/" + name,
	}
}

func TestReplaceListGet(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t, Options{})
	if !s.NeedsRebuild() {
		t.Error("new index should need a rebuild")
	}
	fero := entry("01AAAAAAAAAAAAAAAAAAAAAAAA", "Fenron-dev", "Fero")
	fero.Kind = "own"
	fero.Tags = []string{"medien/beschaffung", "rust"}
	fero.Collections = []string{"fundus"}
	fero.Description = "Medien beschaffen und ordnen"
	fero.Readme = "Fero lädt Bücher herunter"
	fero.Issues = "Absturz beim Zwischenspeichern"
	fero.Relations = []Edge{{To: "github.com/Fenron-dev/Fundus2", Type: "delivers_to", State: "confirmed"}}
	fundus := entry("01BBBBBBBBBBBBBBBBBBBBBBBB", "Fenron-dev", "Fundus2")
	fundus.Tags = []string{"medien"}
	fundus.License = "MIT"
	other := entry("01CCCCCCCCCCCCCCCCCCCCCCCC", "someone", "tool")
	other.Tags = []string{"ttrpg/tool"}

	if err := s.Replace(ctx, []Entry{fero, fundus, other}); err != nil {
		t.Fatal(err)
	}
	if s.NeedsRebuild() {
		t.Error("filled index still needs rebuild")
	}

	all, err := s.List(ctx, Query{})
	if err != nil || len(all) != 3 {
		t.Fatalf("List = %d, %v", len(all), err)
	}
	if all[0].Name != "Fero" || len(all[0].Tags) != 2 || all[0].Relations[0].Type != "delivers_to" {
		t.Errorf("first = %+v", all[0])
	}

	check := func(q Query, want ...string) {
		t.Helper()
		got, err := s.List(ctx, q)
		if err != nil {
			t.Fatalf("List(%+v): %v", q, err)
		}
		var names []string
		for _, e := range got {
			names = append(names, e.Name)
		}
		if len(names) != len(want) {
			t.Fatalf("List(%+v) = %v, want %v", q, names, want)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("List(%+v) = %v, want %v", q, names, want)
			}
		}
	}
	check(Query{Kind: "own"}, "Fero")
	check(Query{Tag: "medien"}, "Fundus2")
	check(Query{Tag: "medien/*"}, "Fero", "Fundus2")
	check(Query{Tag: "ttrpg/*"}, "tool")
	check(Query{Collection: "fundus"}, "Fero")
	check(Query{License: "none"}, "Fero", "tool")
	check(Query{License: "MIT"}, "Fundus2")
	check(Query{Text: "bücher"}, "Fero")
	check(Query{Text: "buch"}, "Fero")              // prefix, diacritics removed
	check(Query{Text: "beschaff"}, "Fero")          // tag words are searchable
	check(Query{Text: "zwischenspeichern"}, "Fero") // issue text (T-9.20)
	check(Query{Text: `fero") (`}, "Fero")
	check(Query{Text: "NEAR( *"})

	got, err := s.Get(ctx, "GITHUB.COM/fenron-dev/fero")
	if err != nil || got.ID != fero.ID {
		t.Fatalf("Get by key = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v", err)
	}

	back, err := s.Backlinks(ctx, fundus)
	if err != nil || len(back) != 1 || back[0].From != fero.ID {
		t.Errorf("Backlinks = %+v, %v", back, err)
	}

	tags, _ := s.Tags(ctx)
	if len(tags) != 4 {
		t.Errorf("Tags = %+v", tags)
	}
	tot, _ := s.Totals(ctx)
	if tot.Repos != 3 {
		t.Errorf("Totals = %+v", tot)
	}
}

func TestUpsertDeleteAndFolderClash(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t, Options{NoWAL: true})
	if s.Journal() != "DELETE" {
		t.Errorf("journal = %s", s.Journal())
	}
	e := entry("01AAAAAAAAAAAAAAAAAAAAAAAA", "a", "Repo")
	if err := s.Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	e.Description = "changed"
	if err := s.Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, e.ID)
	if got.Description != "changed" {
		t.Errorf("upsert did not replace: %q", got.Description)
	}

	clash := entry("01DDDDDDDDDDDDDDDDDDDDDDDD", "A", "repo")
	if err := s.Upsert(ctx, clash); !errors.Is(err, ErrDuplicate) {
		t.Errorf("case clash = %v, want ErrDuplicate", err)
	}
	if _, err := s.ByFolder(ctx, "github.com/A/REPO"); err != nil {
		t.Errorf("ByFolder = %v", err)
	}
	// NFD "é" equals NFC "é" as a folder on macOS.
	nfc := entry("01EEEEEEEEEEEEEEEEEEEEEEEE", "a", "café")
	if err := s.Upsert(ctx, nfc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByFolder(ctx, "github.com/a/café"); err != nil {
		t.Errorf("NFD lookup = %v", err)
	}

	if err := s.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted entry still there: %v", err)
	}
	if res, _ := s.List(ctx, Query{Text: "changed"}); len(res) != 0 {
		t.Error("full-text row of deleted entry remains")
	}
}

func TestOutdatedIndexIsRecreated(t *testing.T) {
	ctx := context.Background()
	s, path := openTemp(t, Options{})
	if err := s.Replace(ctx, []Entry{entry("01AAAAAAAAAAAAAAAAAAAAAAAA", "a", "b")}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Simulate an index of an older schema version.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// An index of version 1 (no issue column) is rebuilt, not migrated:
	// it is derived from the files (invariant 6).
	if _, err := db.Exec(`UPDATE info SET value = '1' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.NeedsRebuild() {
		t.Error("outdated index not reset")
	}
	if all, _ := s2.List(ctx, Query{}); len(all) != 0 {
		t.Errorf("reset index has %d rows", len(all))
	}
}

func TestFTSQuery(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"fero":               `"fero"*`,
		"go-git  v1.2":       `"go-git"* "v1.2"*`,
		`a"b OR c`:           `"a"* "b"* "OR"* "c"*`,
		"--- ...":            "",
		"Grüße, Welt!":       `"Grüße"* "Welt"*`,
		"name:fero col:test": `"name"* "fero"* "col"* "test"*`,
	}
	for in, want := range cases {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenWithSpecialCharactersInPath(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"# Meine Apps", "a?b", "100% sicher", "Grüße & Co", "space dir"} {
		dir := filepath.Join(t.TempDir(), name, "Vault")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "index.db")
		s, err := Open(path, Options{})
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if err := s.Upsert(ctx, entry("01AAAAAAAAAAAAAAAAAAAAAAAA", "o", "n")); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		s.Close()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q: database not at the expected place: %v", name, err)
		}
	}
}
