// SPDX-License-Identifier: GPL-3.0-or-later

// Package store persists metadata. YAML files under meta/ are the source of
// truth; the SQLite database is only a derived index that can always be
// rebuilt from those files (D-004). Unknown YAML fields must survive a
// read/write round trip – that is repometa's job; store never writes YAML.
//
// The driver is modernc.org/sqlite: pure Go (no CGO, so cross-compiling for
// macOS and Windows works on Linux CI) and built with FTS5 for full-text
// search (T-0.4). Because the index is derived, a schema change does not
// migrate rows: an index of another version is dropped and rebuilt
// (Store.NeedsRebuild).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// SchemaVersion of the index tables. Raise it on every change of schemaSQL;
// existing indexes are then rebuilt from the files.
const SchemaVersion = 1

// ErrNotFound is returned when an entry does not exist in the index.
var ErrNotFound = errors.New("not found in index")

const busyTimeout = 5 * time.Second

const schemaSQL = `
CREATE TABLE info (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE repos (
	id TEXT PRIMARY KEY,
	key TEXT NOT NULL,
	key_fold TEXT NOT NULL UNIQUE,
	host TEXT NOT NULL,
	owner TEXT NOT NULL,
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	kind TEXT NOT NULL,
	sync_mode TEXT NOT NULL,
	interval_s INTEGER NOT NULL DEFAULT 0,
	last_success TEXT,
	last_check TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	detected_status TEXT NOT NULL DEFAULT '',
	status_override TEXT NOT NULL DEFAULT '',
	failing_since TEXT,
	description TEXT NOT NULL DEFAULT '',
	language TEXT NOT NULL DEFAULT '',
	license TEXT NOT NULL DEFAULT '',
	topics TEXT NOT NULL DEFAULT '',
	stars INTEGER NOT NULL DEFAULT 0,
	default_branch TEXT NOT NULL DEFAULT '',
	upstream_archived INTEGER NOT NULL DEFAULT 0,
	pushed_at TEXT,
	buildable INTEGER NOT NULL DEFAULT 0,
	created TEXT NOT NULL,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	releases INTEGER NOT NULL DEFAULT 0,
	versions INTEGER NOT NULL DEFAULT 0,
	branches INTEGER NOT NULL DEFAULT 0,
	meta_path TEXT NOT NULL
);
CREATE TABLE tags (repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE, tag TEXT NOT NULL, PRIMARY KEY (repo_id, tag));
CREATE INDEX tags_tag ON tags(tag);
CREATE TABLE collections (repo_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE, collection TEXT NOT NULL, PRIMARY KEY (repo_id, collection));
CREATE INDEX collections_name ON collections(collection);
CREATE TABLE edges (
	from_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
	to_ref TEXT NOT NULL,
	type TEXT NOT NULL,
	state TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (from_id, type, to_ref)
);
CREATE INDEX edges_to ON edges(to_ref);
CREATE VIRTUAL TABLE fts USING fts5(
	repo_id UNINDEXED, name, description, topics, tags, readme, notes,
	tokenize = 'unicode61 remove_diacritics 2'
);
`

// Store is an open index.
type Store struct {
	db      *sql.DB
	journal string
}

// Options control how the database is opened.
type Options struct {
	// NoWAL selects journal_mode=DELETE, required on exFAT/FAT (T-0.9).
	NoWAL bool
}

// Open opens (and if needed creates) the index at path. If the file holds an
// index of another schema version, it is recreated empty and NeedsRebuild
// reports true until Replace has filled it.
func Open(path string, opts Options) (*Store, error) {
	journal := "WAL"
	if opts.NoWAL {
		journal = "DELETE"
	}
	db, err := sql.Open("sqlite", dsnOf(path, journal))
	if err != nil {
		return nil, fmt.Errorf("open index %s: %w", path, err)
	}
	// One writer at a time keeps SQLite free of "database is locked" inside
	// this process; readers wait briefly instead.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, journal: journal}
	if err := s.ensureSchema(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Journal returns the journal mode in use (WAL or DELETE).
func (s *Store) Journal() string { return s.journal }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ensureSchema(path string) error {
	v, err := s.info("schema")
	if err == nil && v == strconv.Itoa(SchemaVersion) {
		return nil
	}
	// Empty, foreign or outdated: the index is derived, so start over.
	if err := s.db.Close(); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reset index: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsnOf(path, s.journal))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	s.db = db
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO info(key, value) VALUES ('schema', ?), ('built', '')`, strconv.Itoa(SchemaVersion)); err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	return tx.Commit()
}

func dsnOf(path, journal string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=journal_mode(%s)&_pragma=synchronous(NORMAL)",
		path, busyTimeout.Milliseconds(), journal)
}

func (s *Store) info(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM info WHERE key = ?`, key).Scan(&v)
	return v, err
}

// NeedsRebuild reports whether the index has never been filled from the
// files (new, or reset after a schema change).
func (s *Store) NeedsRebuild() bool {
	v, err := s.info("built")
	return err != nil || v == ""
}

// Replace drops all entries and inserts entries, in one transaction. It is
// the reindex operation.
func (s *Store) Replace(ctx context.Context, entries []Entry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM edges`, `DELETE FROM tags`, `DELETE FROM collections`, `DELETE FROM fts`, `DELETE FROM repos`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("clear index: %w", err)
		}
	}
	for i := range entries {
		if err := insert(ctx, tx, &entries[i]); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE info SET value = ? WHERE key = 'built'`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// Upsert replaces one entry.
func (s *Store) Upsert(ctx context.Context, e Entry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := remove(ctx, tx, e.ID); err != nil {
		return err
	}
	if err := insert(ctx, tx, &e); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete removes one entry.
func (s *Store) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := remove(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func remove(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM fts WHERE repo_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM repos WHERE id = ?`, id)
	return err
}

func insert(ctx context.Context, tx *sql.Tx, e *Entry) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO repos (
		id, key, key_fold, host, owner, name, url, kind, sync_mode, interval_s,
		last_success, last_check, last_error, status, detected_status, status_override,
		failing_since, description, language, license, topics, stars, default_branch,
		upstream_archived, pushed_at, buildable, created, size_bytes, releases, versions,
		branches, meta_path)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Key, FoldKey(e.Key), e.Host, e.Owner, e.Name, e.URL, e.Kind, e.SyncMode,
		int64(e.Interval/time.Second), timeOrNil(e.LastSuccess), timeOrNil(e.LastCheck), e.LastError,
		e.Status, e.DetectedStatus, e.StatusOverride, timeOrNil(e.FailingSince),
		e.Description, e.Language, e.License, strings.Join(e.Topics, " "), e.Stars, e.DefaultBranch,
		e.UpstreamArchived, timeOrNil(e.PushedAt), e.Buildable, e.Created.UTC().Format(time.RFC3339),
		e.SizeBytes, e.Releases, e.Versions, e.Branches, e.MetaPath)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("index %s: %w", e.Key, ErrDuplicate)
		}
		return fmt.Errorf("index %s: %w", e.Key, err)
	}
	for _, t := range e.Tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags(repo_id, tag) VALUES (?, ?)`, e.ID, t); err != nil {
			return err
		}
	}
	for _, c := range e.Collections {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO collections(repo_id, collection) VALUES (?, ?)`, e.ID, c); err != nil {
			return err
		}
	}
	for _, ed := range e.Relations {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO edges(from_id, to_ref, type, state, source) VALUES (?,?,?,?,?)`,
			e.ID, ed.To, ed.Type, ed.State, ed.Source); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fts(repo_id, name, description, topics, tags, readme, notes) VALUES (?,?,?,?,?,?,?)`,
		e.ID, e.Owner+" "+e.Name, e.Description, strings.Join(e.Topics, " "),
		strings.ReplaceAll(strings.Join(e.Tags, " "), "/", " "), e.Readme, e.Notes)
	return err
}

// ErrDuplicate is returned when two repositories would share one folder
// (same host name, owner and name, ignoring case – T-1.12, D-018).
var ErrDuplicate = errors.New("another repository uses the same folder")

// FoldKey is the case-insensitive form of a key. Folders that differ only in
// case are the same folder on macOS and Windows.
func FoldKey(key string) string { return strings.ToLower(normalize(key)) }

func timeOrNil(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return nil
	}
	return &t
}
