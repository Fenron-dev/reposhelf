// SPDX-License-Identifier: GPL-3.0-or-later

package repometa

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Fenron-dev/reposhelf/internal/safepath"
)

const (
	// minSyncInterval protects upstream hosts and API limits (60/h without
	// token) from overly eager schedules.
	minSyncInterval = 5 * time.Minute
	maxPort         = 65535
	// minTargetSegments is host/owner/name.
	minTargetSegments = 3
)

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var relationTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// URL schemes a source may use. Plain local paths are excluded: they would
// let a repo.yaml point RepoShelf at arbitrary folders on the machine.
var allowedSchemes = []string{"https", "http", "ssh", "git"}

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid repo.yaml")

// FieldError describes one invalid field.
type FieldError struct {
	Field   string
	Problem string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Problem }

func (e *FieldError) Unwrap() error { return ErrInvalid }

// Validate checks r against docs/SCHEMA.md and returns all problems at once
// (joined), so a hand editor can fix them in one go.
func (r *Repo) Validate() error {
	v := &validator{}

	if r.Schema != CurrentSchema {
		v.addf("schema", "must be %d, is %d", CurrentSchema, r.Schema)
	}
	if !isULID(r.ID) {
		v.add("id", "must be a ULID (26 characters, Crockford base32, upper case)")
	}
	v.source(r.Source)

	switch r.Kind {
	case KindOwn, KindMirror, KindFork:
	default:
		v.addf("kind", "must be own, mirror or fork, is %q", r.Kind)
	}
	switch {
	case r.Kind == KindFork && r.ForkOf == nil:
		v.add("fork_of", "is required for kind fork")
	case r.Kind != KindFork && r.ForkOf != nil:
		v.add("fork_of", "is only allowed for kind fork")
	case r.ForkOf != nil:
		v.target("fork_of", *r.ForkOf, r)
	}

	v.sync(r.Sync)

	if s := r.StatusOverride; s != nil {
		switch *s {
		case UpstreamOK, UpstreamDegraded, UpstreamGone, UpstreamArchived:
		default:
			v.addf("status_override", "must be null, ok, degraded, gone or archived, is %q", *s)
		}
	}

	v.list("tags", r.Tags, validTag)
	v.list("collections", r.Collections, validSegment)
	v.relations(r)
	v.retention(r.Retention)

	if r.Buildable.CapsulesKeep != nil && *r.Buildable.CapsulesKeep < 0 {
		v.add("buildable.capsules_keep", "must not be negative")
	}
	if r.License.SPDX != nil && strings.TrimSpace(*r.License.SPDX) == "" {
		v.add("license.spdx", "must be null or a SPDX identifier")
	}
	if r.Created.IsZero() {
		v.add("created", "is required")
	}
	return v.err()
}

type validator struct{ errs []error }

func (v *validator) add(field, problem string) {
	v.errs = append(v.errs, &FieldError{Field: field, Problem: problem})
}

func (v *validator) addf(field, format string, args ...any) {
	v.add(field, fmt.Sprintf(format, args...))
}

func (v *validator) err() error { return errors.Join(v.errs...) }

func (v *validator) source(s Source) {
	if s.URL == "" && s.Host == LocalHost {
		// Repositories that exist only in RepoShelf have no upstream.
	} else if s.URL == "" {
		v.add("source.url", "is required")
	} else if u, err := url.Parse(s.URL); err != nil || u.Host == "" {
		v.add("source.url", "must be an absolute URL with host")
	} else {
		if !slices.Contains(allowedSchemes, u.Scheme) {
			v.addf("source.url", "scheme %q not allowed (use %s)", u.Scheme, strings.Join(allowedSchemes, ", "))
		}
		// Tokens must never land in YAML (AGENTS.md security). ssh needs a
		// user name (git@…), but never a password.
		if u.User != nil {
			if _, hasPw := u.User.Password(); hasPw || u.Scheme != "ssh" {
				v.add("source.url", "must not contain credentials")
			}
		}
	}
	if p := validHost(s.Host); p != "" {
		v.add("source.host", p)
	}
	if s.Owner == "" {
		v.add("source.owner", "is required")
	}
	for _, seg := range strings.Split(s.Owner, "/") {
		if p := validSegment(seg); p != "" && s.Owner != "" {
			v.add("source.owner", p)
			break
		}
	}
	if p := validSegment(s.Name); p != "" {
		v.add("source.name", p)
	}
}

func (v *validator) sync(s Sync) {
	switch s.Mode {
	case SyncOnce, SyncManual:
	case SyncFollow:
		if s.Interval == nil {
			v.add("sync.interval", "is required for mode follow")
		}
	default:
		v.addf("sync.mode", "must be once, follow or manual, is %q", s.Mode)
	}
	if s.Interval != nil && time.Duration(*s.Interval) < minSyncInterval {
		v.addf("sync.interval", "must be at least %s", Duration(minSyncInterval))
	}
}

func (v *validator) relations(r *Repo) {
	seen := map[string]bool{}
	for i, rel := range r.Relations {
		field := fmt.Sprintf("relations[%d]", i)
		if !relationTypePattern.MatchString(rel.Type) {
			v.add(field+".type", "must be lower snake_case, e.g. depends_on")
		}
		v.target(field+".target", rel.Target, r)
		switch rel.State {
		case RelationConfirmed, RelationSuggested, RelationRejected:
		default:
			v.addf(field+".state", "must be confirmed, suggested or rejected, is %q", rel.State)
		}
		key := relationKey(rel.Type, rel.Target)
		if seen[key] {
			v.add(field, "duplicate relation (same type and target)")
		}
		seen[key] = true
	}
}

// target checks a reference to another repository: either its ULID or its
// host/owner/name path.
func (v *validator) target(field, ref string, self *Repo) {
	if isULID(ref) {
		if ref == self.ID {
			v.add(field, "must not point to the repository itself")
		}
		return
	}
	segs := strings.Split(ref, "/")
	if len(segs) < minTargetSegments {
		v.add(field, "must be a ULID or host/owner/name")
		return
	}
	for i, seg := range segs {
		check := validSegment
		if i == 0 {
			check = validHost
		}
		if p := check(seg); p != "" {
			v.add(field, p)
			return
		}
	}
	if ref == self.Source.Host+"/"+self.Source.Owner+"/"+self.Source.Name {
		v.add(field, "must not point to the repository itself")
	}
}

func (v *validator) retention(r Retention) {
	if r.ReleasesKeep != nil && *r.ReleasesKeep < 0 {
		v.add("retention.releases_keep", "must not be negative")
	}
	v.list("retention.pinned", r.Pinned, func(s string) string {
		if strings.TrimSpace(s) == "" || hasControl(s) {
			return "must be a version name"
		}
		return ""
	})
	for field, d := range map[string]*Duration{
		"retention.snapshots.every":             r.Snapshots.Every,
		"retention.snapshots.keep_for":          r.Snapshots.KeepFor,
		"retention.force_push_backups.keep_for": r.ForcePushBackups.KeepFor,
	} {
		if d != nil && *d <= 0 {
			v.add(field, "must be greater than zero or null")
		}
	}
}

// list validates each entry and rejects duplicates.
func (v *validator) list(field string, items []string, check func(string) string) {
	seen := map[string]bool{}
	for i, s := range items {
		if p := check(s); p != "" {
			v.addf(fmt.Sprintf("%s[%d]", field, i), "%q: %s", s, p)
		}
		if seen[s] {
			v.addf(fmt.Sprintf("%s[%d]", field, i), "duplicate %q", s)
		}
		seen[s] = true
	}
}

// validSegment returns a problem description, or "" if s may become a single
// path segment. It applies the same rules as every path built from repo.yaml
// (safepath), so a file that validates can always be stored.
func validSegment(s string) string {
	var se *safepath.SegmentError
	if errors.As(safepath.CheckSegment(s), &se) {
		return se.Problem
	}
	return ""
}

// validHost accepts hostname[:port] (D-018). Only the hostname becomes a
// folder name, so it must be a valid segment; the port must be a number.
func validHost(s string) string {
	name, port, hasPort := strings.Cut(s, ":")
	if p := validSegment(name); p != "" {
		return p
	}
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > maxPort || strconv.Itoa(n) != port {
			return "port must be a number from 1 to 65535 (IPv6 addresses are not supported)"
		}
	}
	return ""
}

// validTag allows hierarchy with "/" (ttrpg/tool); "*" is reserved for
// filters (ttrpg/*).
func validTag(s string) string {
	if strings.Contains(s, "*") {
		return "must not contain * (reserved for filters)"
	}
	for _, seg := range strings.Split(s, "/") {
		if p := validSegment(seg); p != "" {
			return "each level " + p
		}
	}
	return ""
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

func isULID(s string) bool {
	const ulidLen = 26
	// The first character carries only 3 bits of the 48-bit timestamp.
	const maxFirst = '7'
	if len(s) != ulidLen || s[0] > maxFirst {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(ulidAlphabet, rune(s[i])) {
			return false
		}
	}
	return true
}

func relationKey(typ, target string) string { return typ + "\x00" + target }
