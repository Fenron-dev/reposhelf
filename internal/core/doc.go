// SPDX-License-Identifier: GPL-3.0-or-later

// Package core holds the domain logic of RepoShelf: repositories, the mirror
// engine, metadata, retention rules, trash and search.
//
// It is the centre of the dependency graph. It must not import web, api,
// githttp, mcp or any desktop shell (Wails); those layers depend on core,
// never the other way round (AGENTS.md, D-003).
package core
