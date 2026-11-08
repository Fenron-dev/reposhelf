// SPDX-License-Identifier: GPL-3.0-or-later

package core

import "io"

var ioCopy = io.Copy

// capsuleCandidates proposes build capsules beyond capsules_keep (Phase 6).
func (l *Library) capsuleCandidates(r *Repo, ret Retention) []CleanupItem { return nil }

// buildCandidates proposes old build results beyond builds_keep (Phase 7).
func (l *Library) buildCandidates() []CleanupItem { return nil }
