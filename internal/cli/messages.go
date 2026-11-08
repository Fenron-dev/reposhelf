// SPDX-License-Identifier: GPL-3.0-or-later

package cli

// User-facing texts are German and live in one place (AGENTS.md, D-009) so
// they can be reviewed and later translated without touching the logic.
const (
	msgUsage = `RepoShelf – Archiv und Bereitstellung für Git-Repositories

Aufruf:
  reposhelf <befehl> [optionen]

Befehle:
  version   Version anzeigen
  help      Diese Hilfe anzeigen
`
	msgVersion        = "reposhelf %s\n"
	msgUnknownCommand = "Unbekannter Befehl: %q\n\n"
)
