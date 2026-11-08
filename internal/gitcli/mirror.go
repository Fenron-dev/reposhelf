// SPDX-License-Identifier: GPL-3.0-or-later

package gitcli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BackupNamespace holds the ref state before a history rewrite upstream
// (KONZEPT 6.2, AGENTS.md invariant 1): refs/reposhelf/backup/<UTC time>/<ref>.
const BackupNamespace = "refs/reposhelf/backup/"

const backupTimeFormat = "20060102T150405Z"

// RefChange is one ref that moved.
type RefChange struct {
	Ref, Old, New string
}

// MirrorResult tells what a sync did.
type MirrorResult struct {
	New       []RefChange // refs that did not exist here
	Updated   []RefChange // fast-forwards (no history lost)
	Rewritten []RefChange // force-pushed or moved tags: old state backed up
	// Deleted lists refs that are gone upstream. They stay here.
	Deleted []string
	// Backup is the backup namespace used, or "" if nothing was rewritten.
	Backup string
	// DefaultBranch is the remote's default branch, if it could be read.
	DefaultBranch string
}

// Changed reports whether any ref moved.
func (r MirrorResult) Changed() bool {
	return len(r.New)+len(r.Updated)+len(r.Rewritten) > 0
}

// Mirror brings the bare repository dir up to date with its origin without
// ever losing history:
//
//  1. Compare remote refs (ls-remote) with local ones. Nothing changed →
//     done, no fetch (the common case costs one round trip).
//  2. Fetch without force: fast-forwards and new refs arrive, rewrites are
//     rejected by git itself.
//  3. For each rejected ref, store the old value under BackupNamespace,
//     then fetch exactly that ref with force.
//
// Refs deleted upstream are kept (no prune).
func (g *Git) Mirror(ctx context.Context, dir, url string, auth *Auth, now time.Time) (MirrorResult, error) {
	if err := g.SetURL(ctx, dir, url); err != nil {
		return MirrorResult{}, err
	}
	return g.mirror(ctx, dir, "origin", url, auth, now)
}

// MirrorFrom fetches from url (e.g. another RepoShelf instance) with the
// same protection as Mirror, without touching the configured origin.
func (g *Git) MirrorFrom(ctx context.Context, dir, url string, auth *Auth, now time.Time) (MirrorResult, error) {
	return g.mirror(ctx, dir, url, url, auth, now)
}

var mirrorRefspecs = []string{"refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*", "refs/notes/*:refs/notes/*"}

func (g *Git) mirror(ctx context.Context, dir, from, url string, auth *Auth, now time.Time) (MirrorResult, error) {
	var res MirrorResult
	remote, err := g.LsRemote(ctx, url, auth)
	if err != nil {
		return res, err
	}
	res.DefaultBranch = g.RemoteHead(ctx, url, auth)
	before, err := g.LocalRefs(ctx, dir)
	if err != nil {
		return res, err
	}
	for ref := range before {
		if _, ok := remote[ref]; !ok {
			res.Deleted = append(res.Deleted, ref)
		}
	}
	sort.Strings(res.Deleted)

	pending := false
	for ref, oid := range remote {
		if before[ref] != oid {
			pending = true
			break
		}
	}
	if !pending {
		return res, g.SetHead(ctx, dir, res.DefaultBranch)
	}

	// Step 2: safe fetch. git exits non-zero when it rejects a ref; that
	// is expected and handled below, any other failure is not.
	// Not --quiet: git reports rejections only without it.
	fetch := append([]string{"fetch", "--no-progress", "--no-tags", "--end-of-options", from}, mirrorRefspecs...)
	if _, err := g.Run(ctx, dir, auth, fetch...); err != nil {
		var ge *Error
		if !errors.As(err, &ge) || !strings.Contains(ge.Stderr, "rejected") {
			return res, err
		}
	}
	after, err := g.LocalRefs(ctx, dir)
	if err != nil {
		return res, err
	}

	var force []string
	for _, ref := range sortedKeys(remote) {
		oid := remote[ref]
		old, existed := before[ref]
		switch {
		case after[ref] == oid && !existed:
			res.New = append(res.New, RefChange{Ref: ref, New: oid})
		case after[ref] == oid && old != oid:
			res.Updated = append(res.Updated, RefChange{Ref: ref, Old: old, New: oid})
		case after[ref] != oid && existed:
			res.Rewritten = append(res.Rewritten, RefChange{Ref: ref, Old: after[ref], New: oid})
			force = append(force, "+"+ref+":"+ref)
		case after[ref] != oid:
			// New upstream ref that did not arrive: moved during the sync.
			return res, fmt.Errorf("ref %s did not arrive, upstream changed during sync", ref)
		}
	}
	if len(force) == 0 {
		return res, g.SetHead(ctx, dir, res.DefaultBranch)
	}

	// Step 3: back up, then force.
	res.Backup = BackupNamespace + now.UTC().Format(backupTimeFormat) + "/"
	for _, c := range res.Rewritten {
		if _, err := g.Run(ctx, dir, nil, "update-ref", "-m", "reposhelf backup before rewrite", res.Backup+c.Ref, c.Old); err != nil {
			return res, fmt.Errorf("back up %s: %w", c.Ref, err)
		}
	}
	args := append([]string{"fetch", "--quiet", "--no-tags", "--end-of-options", from}, force...)
	if _, err := g.Run(ctx, dir, auth, args...); err != nil {
		return res, err
	}
	return res, g.SetHead(ctx, dir, res.DefaultBranch)
}

// Backup is one saved ref state.
type Backup struct {
	Time time.Time
	Refs map[string]string // original ref → object ID
}

// Backups lists the force-push backups of dir, newest first.
func (g *Git) Backups(ctx context.Context, dir string) ([]Backup, error) {
	out, err := g.Run(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", BackupNamespace)
	if err != nil {
		return nil, err
	}
	byTime := map[string]*Backup{}
	for name, oid := range parseRefs(out, " ") {
		rest := strings.TrimPrefix(name, BackupNamespace)
		stamp, ref, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		t, err := time.Parse(backupTimeFormat, stamp)
		if err != nil {
			continue
		}
		b := byTime[stamp]
		if b == nil {
			b = &Backup{Time: t, Refs: map[string]string{}}
			byTime[stamp] = b
		}
		b.Refs[ref] = oid
	}
	var list []Backup
	for _, b := range byTime {
		list = append(list, *b)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Time.After(list[j].Time) })
	return list, nil
}

// MoveRefs renames refs (e.g. a backup into the trash namespace) without
// losing them: create the new ref, then delete the old.
func (g *Git) MoveRefs(ctx context.Context, dir, fromPrefix, toPrefix string) (int, error) {
	out, err := g.Run(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", fromPrefix)
	if err != nil {
		return 0, err
	}
	refs := parseRefs(out, " ")
	for name, oid := range refs {
		target := toPrefix + strings.TrimPrefix(name, fromPrefix)
		if _, err := g.Run(ctx, dir, nil, "update-ref", target, oid); err != nil {
			return 0, err
		}
		if _, err := g.Run(ctx, dir, nil, "update-ref", "-d", name, oid); err != nil {
			return 0, err
		}
	}
	return len(refs), nil
}

// DeleteRefs removes all refs below prefix. Only for RepoShelf's own
// namespaces after the user confirmed (trash emptying).
func (g *Git) DeleteRefs(ctx context.Context, dir, prefix string) error {
	if !strings.HasPrefix(prefix, "refs/reposhelf/") {
		return fmt.Errorf("refusing to delete refs outside refs/reposhelf/: %s", prefix)
	}
	out, err := g.Run(ctx, dir, nil, "for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return err
	}
	for name, oid := range parseRefs(out, " ") {
		if _, err := g.Run(ctx, dir, nil, "update-ref", "-d", name, oid); err != nil {
			return err
		}
	}
	return nil
}

// FetchLFS downloads all LFS objects of the mirror.
func (g *Git) FetchLFS(ctx context.Context, dir string, auth *Auth) error {
	_, err := g.Run(ctx, dir, auth, "lfs", "fetch", "--all", "origin")
	return err
}

func sortedKeys(m Refs) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
