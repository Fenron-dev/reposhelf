// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Fenron-dev/reposhelf/internal/artifact"
)

var ioCopy = io.Copy

// ItemBlobs are files in the shared dependency cache no capsule uses.
const ItemBlobs = "unused_blobs"

// capsuleCandidates proposes capsules beyond capsules_keep (pinned versions
// keep theirs).
func (l *Library) capsuleCandidates(r *Repo, ret Retention) []CleanupItem {
	dir, err := artifact.CapsuleDir(l.Layout.Root, r.R().Source)
	if err != nil {
		return nil
	}
	var items []CleanupItem
	kept := 0
	for _, c := range artifact.ReadCapsules(dir) {
		if slices.Contains(ret.Pinned, c.Version) {
			continue
		}
		if kept < ret.CapsulesKeep {
			kept++
			continue
		}
		items = append(items, CleanupItem{
			ID: itemID(ItemCapsule, r.R().ID, c.Version), Kind: ItemCapsule, RepoID: r.R().ID, Key: r.Entry.Key,
			Label: "Build-Kapsel " + c.Version, Reason: fmt.Sprintf("mehr als %d Kapseln", ret.CapsulesKeep),
			Size: c.Size, Since: c.Created, Path: c.Path,
		})
	}
	return items
}

// buildCandidates proposes finished builds beyond builds_keep per repo.
func (l *Library) buildCandidates() []CleanupItem {
	keep := l.Config().Retention.BuildsKeep
	perRepo := map[string]int{}
	var items []CleanupItem
	for _, b := range artifact.ListBuilds(l.Layout.Root) {
		if !b.Done() {
			continue
		}
		perRepo[b.RepoID]++
		if perRepo[b.RepoID] <= keep {
			continue
		}
		var size int64
		for _, a := range b.Artifacts {
			size += a.Size
		}
		items = append(items, CleanupItem{
			ID: itemID(ItemBuild, b.ID), Kind: ItemBuild, RepoID: b.RepoID, Key: b.Key,
			Label: "Build " + b.Target + " (" + b.Ref + ")", Reason: fmt.Sprintf("mehr als %d Builds", keep),
			Size: size, Since: b.Created, Path: b.Dir,
		})
	}
	return items
}

// blobCandidates proposes cache files that no capsule references.
func (l *Library) blobCandidates() []CleanupItem {
	blobs := artifact.BlobsOf(l.Layout.Root)
	unused, size := blobs.Unused(artifact.Referenced(l.Layout.Path("capsules")))
	if len(unused) == 0 {
		return nil
	}
	return []CleanupItem{{
		ID: itemID(ItemBlobs, fmt.Sprint(len(unused)), fmt.Sprint(size)), Kind: ItemBlobs,
		Label: fmt.Sprintf("Unbenutzte Abhängigkeiten (%d Dateien)", len(unused)), Key: "Gemeinsamer Cache",
		Reason: "von keiner Kapsel mehr verwendet", Size: size, Path: strings.Join(unused, "\n"),
	}}
}
