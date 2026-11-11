// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Entry is one repository as the index knows it: the fields of repo.yaml
// that lists and filters need, plus derived data (upstream snapshot, README,
// notes, sizes).
type Entry struct {
	ID, Key, Host, Owner, Name, URL string
	Kind, SyncMode                  string
	Interval                        time.Duration
	LastSuccess, LastCheck          *time.Time
	LastError                       string
	// Status is the effective status: override, else detected (Fero rule).
	Status, DetectedStatus, StatusOverride string
	FailingSince                           *time.Time
	Description, Language, License         string
	Topics                                 []string
	Stars                                  int
	DefaultBranch                          string
	UpstreamArchived                       bool
	PushedAt                               *time.Time
	Buildable                              bool
	Created                                time.Time
	SizeBytes                              int64
	Releases, Versions, Branches           int
	MetaPath                               string

	Tags, Collections []string
	Relations         []Edge

	// Only for the full-text index; not returned by queries.
	Readme, Notes string
	// Issues is the text of the repository's issues (search only, T-9.20).
	Issues string
}

// Edge is a relation from one repository to another (KONZEPT 6.7).
type Edge struct {
	From, To            string // To is a ULID or host/owner/name
	Type, State, Source string
}

// Sort orders of List.
const (
	SortName    = "name"
	SortUpdated = "updated"
	SortAdded   = "added"
	SortSize    = "size"
)

// Query filters List. Empty fields do not filter.
type Query struct {
	Text       string // full-text search
	Kind       string
	Tag        string // exact, or "prefix/*" for a whole branch of the hierarchy
	Collection string
	Status     string
	License    string // "none" selects repositories without detected license
	Language   string
	Host       string
	Buildable  bool
	Sort       string
	Limit      int
}

const selectCols = `r.id, r.key, r.host, r.owner, r.name, r.url, r.kind, r.sync_mode, r.interval_s,
	r.last_success, r.last_check, r.last_error, r.status, r.detected_status, r.status_override,
	r.failing_since, r.description, r.language, r.license, r.topics, r.stars, r.default_branch,
	r.upstream_archived, r.pushed_at, r.buildable, r.created, r.size_bytes, r.releases, r.versions,
	r.branches, r.meta_path`

// List returns the entries matching q.
func (s *Store) List(ctx context.Context, q Query) ([]Entry, error) {
	var where []string
	var args []any
	from := "repos r"
	if t := ftsQuery(q.Text); t != "" {
		from = "repos r JOIN fts ON fts.repo_id = r.id"
		where = append(where, "fts MATCH ?")
		args = append(args, t)
	}
	if q.Kind != "" {
		where, args = append(where, "r.kind = ?"), append(args, q.Kind)
	}
	if q.Status != "" {
		where, args = append(where, "r.status = ?"), append(args, q.Status)
	}
	if q.Host != "" {
		where, args = append(where, "r.host = ?"), append(args, q.Host)
	}
	if q.Language != "" {
		where, args = append(where, "r.language = ?"), append(args, q.Language)
	}
	switch q.License {
	case "":
	case "none":
		where = append(where, "r.license = ''")
	default:
		where, args = append(where, "r.license = ?"), append(args, q.License)
	}
	if q.Buildable {
		where = append(where, "r.buildable = 1")
	}
	if q.Tag != "" {
		if prefix, ok := strings.CutSuffix(q.Tag, "/*"); ok {
			where = append(where, "EXISTS (SELECT 1 FROM tags t WHERE t.repo_id = r.id AND (t.tag = ? OR t.tag LIKE ? ESCAPE '\\'))")
			args = append(args, prefix, escapeLike(prefix)+"/%")
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM tags t WHERE t.repo_id = r.id AND t.tag = ?)")
			args = append(args, q.Tag)
		}
	}
	if q.Collection != "" {
		where = append(where, "EXISTS (SELECT 1 FROM collections c WHERE c.repo_id = r.id AND c.collection = ?)")
		args = append(args, q.Collection)
	}
	sqlText := "SELECT " + selectCols + " FROM " + from
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	switch q.Sort {
	case SortUpdated:
		sqlText += " ORDER BY coalesce(r.last_success, r.created) DESC"
	case SortAdded:
		sqlText += " ORDER BY r.created DESC"
	case SortSize:
		sqlText += " ORDER BY r.size_bytes DESC"
	default:
		if q.Text != "" && from != "repos r" {
			sqlText += " ORDER BY rank"
		} else {
			sqlText += " ORDER BY lower(r.name), lower(r.owner)"
		}
	}
	if q.Limit > 0 {
		sqlText += fmt.Sprintf(" LIMIT %d", q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fill(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns one entry by ID or by key (host/owner/name, any case).
func (s *Store) Get(ctx context.Context, ref string) (Entry, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+selectCols+" FROM repos r WHERE r.id = ? OR r.key_fold = ?", ref, FoldKey(ref))
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, fmt.Errorf("%s: %w", ref, ErrNotFound)
	}
	if err != nil {
		return Entry{}, err
	}
	return e, s.fill(ctx, &e)
}

// ByFolder returns the entry occupying the folder of key, ignoring case and
// Unicode normalisation; ErrNotFound if the folder is free.
func (s *Store) ByFolder(ctx context.Context, key string) (Entry, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+selectCols+" FROM repos r WHERE r.key_fold = ?", FoldKey(key))
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	return e, err
}

type scanner interface{ Scan(...any) error }

func scanEntry(r scanner) (Entry, error) {
	var e Entry
	var interval int64
	var lastSuccess, lastCheck, failingSince, pushedAt sql.NullString
	var topics, created string
	err := r.Scan(&e.ID, &e.Key, &e.Host, &e.Owner, &e.Name, &e.URL, &e.Kind, &e.SyncMode, &interval,
		&lastSuccess, &lastCheck, &e.LastError, &e.Status, &e.DetectedStatus, &e.StatusOverride,
		&failingSince, &e.Description, &e.Language, &e.License, &topics, &e.Stars, &e.DefaultBranch,
		&e.UpstreamArchived, &pushedAt, &e.Buildable, &created, &e.SizeBytes, &e.Releases, &e.Versions,
		&e.Branches, &e.MetaPath)
	if err != nil {
		return e, err
	}
	e.Interval = time.Duration(interval) * time.Second
	e.LastSuccess, e.LastCheck = parseTime(lastSuccess), parseTime(lastCheck)
	e.FailingSince, e.PushedAt = parseTime(failingSince), parseTime(pushedAt)
	if topics != "" {
		e.Topics = strings.Fields(topics)
	}
	if t := parseTime(sql.NullString{String: created, Valid: true}); t != nil {
		e.Created = *t
	}
	return e, nil
}

// fill loads tags, collections and relations of e.
func (s *Store) fill(ctx context.Context, e *Entry) error {
	var err error
	if e.Tags, err = s.strings(ctx, `SELECT tag FROM tags WHERE repo_id = ? ORDER BY tag`, e.ID); err != nil {
		return err
	}
	if e.Collections, err = s.strings(ctx, `SELECT collection FROM collections WHERE repo_id = ? ORDER BY collection`, e.ID); err != nil {
		return err
	}
	e.Relations, err = s.edges(ctx, `SELECT from_id, to_ref, type, state, source FROM edges WHERE from_id = ? ORDER BY type, to_ref`, e.ID)
	return err
}

func (s *Store) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) edges(ctx context.Context, q string, args ...any) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.From, &e.To, &e.Type, &e.State, &e.Source); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Backlinks returns relations of other repositories that point to e, by ID
// or by its key (KONZEPT 6.7). Rejected suggestions are left out.
func (s *Store) Backlinks(ctx context.Context, e Entry) ([]Edge, error) {
	return s.edges(ctx, `SELECT from_id, to_ref, type, state, source FROM edges
		WHERE (to_ref = ? OR lower(to_ref) = ?) AND state != 'rejected' ORDER BY type, from_id`, e.ID, FoldKey(e.Key))
}

// Count is a name with the number of repositories using it.
type Count struct {
	Name  string
	Count int
}

// Tags returns all tags with their usage, sorted by name.
func (s *Store) Tags(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT tag, count(*) FROM tags GROUP BY tag ORDER BY tag`)
}

// Collections returns all collections with their size.
func (s *Store) Collections(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT collection, count(*) FROM collections GROUP BY collection ORDER BY collection`)
}

// Kinds counts repositories by kind.
func (s *Store) Kinds(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT kind, count(*) FROM repos GROUP BY kind ORDER BY kind`)
}

// Statuses counts repositories by effective status.
func (s *Store) Statuses(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT status, count(*) FROM repos GROUP BY status ORDER BY status`)
}

// Hosts counts repositories by host.
func (s *Store) Hosts(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT host, count(*) FROM repos GROUP BY host ORDER BY host`)
}

// Languages counts repositories by main language.
func (s *Store) Languages(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT language, count(*) FROM repos WHERE language != '' GROUP BY language ORDER BY count(*) DESC, language`)
}

func (s *Store) counts(ctx context.Context, q string) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Totals are numbers for the library header.
type Totals struct {
	Repos     int
	SizeBytes int64
}

// Totals sums up the index.
func (s *Store) Totals(ctx context.Context) (Totals, error) {
	var t Totals
	err := s.db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(size_bytes), 0) FROM repos`).Scan(&t.Repos, &t.SizeBytes)
	return t, err
}

// ftsQuery turns user input into an FTS5 query: every word becomes a quoted
// prefix term, so operators typed by the user (or by an agent) are plain
// text and cannot break the query.
func ftsQuery(text string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' && r != '.'
	}) {
		w = strings.Trim(w, "-.")
		if w == "" {
			continue
		}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
	}
	return strings.Join(terms, " ")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func normalize(s string) string { return norm.NFC.String(s) }
