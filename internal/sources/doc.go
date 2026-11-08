// SPDX-License-Identifier: GPL-3.0-or-later

// Package sources talks to upstream hosts (GitHub first, later GitLab,
// Gitea/Forgejo, plain Git URLs). It turns a repository URL into metadata and
// clone instructions. Names coming from the network are validated before they
// are ever used as path segments.
package sources
