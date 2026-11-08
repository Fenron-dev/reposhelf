// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"github.com/Fenron-dev/reposhelf/internal/config"
	"github.com/Fenron-dev/reposhelf/internal/datadir"
)

// User-facing texts are German and live in one place (AGENTS.md, D-009) so
// they can be reviewed and later translated without touching the logic.
const (
	msgUsage = `RepoShelf – Archiv und Bereitstellung für Git-Repositories

Aufruf:
  reposhelf <befehl> [optionen]

Befehle:
  config    Datenordner und wirksame Einstellungen anzeigen
  version   Version anzeigen
  help      Diese Hilfe anzeigen

Optionen:
  --data <ordner>   Datenordner festlegen. Ohne Angabe gilt der Reihe nach:
                    Umgebungsvariable ` + datadir.EnvVar + `, der gewählte Datenordner,
                    der Ordner ` + datadir.PortableDirName + ` neben dem Programm.

Umgebungsvariablen:
  ` + datadir.EnvVar + `      Datenordner (Docker: /data)
  ` + config.EnvListen + `    Adresse der Weboberfläche, z. B. :7373
`
	msgVersion        = "reposhelf %s\n"
	msgUnknownCommand = "Unbekannter Befehl: %q\n\n"
	msgExtraArgs      = "Unerwartete Angabe: %q\n\n"

	msgNotConfigured = `Kein Datenordner eingerichtet.
Lege ihn mit --data <ordner> oder der Umgebungsvariable ` + datadir.EnvVar + ` fest,
oder lege für den portablen Betrieb den Ordner ` + datadir.PortableDirName + ` neben dem Programm an.
`
	msgUnreachable = `Datenordner nicht erreichbar: %s
Quelle: %s
Grund:  %s
RepoShelf weicht nicht auf einen anderen Ort aus.
`
	msgConfigInvalid = "Konfiguration fehlerhaft: %v\n"
	msgConfigWarning = "Warnung in %s: %s\n"

	msgConfigReport = `Datenordner:    %s
Quelle:         %s
Konfiguration:  %s%s
Adresse:        %s
`
	msgConfigFileMissing = " (nicht vorhanden – Standardwerte)"
)

// sourceLabels names the data directory sources for the user.
var sourceLabels = map[datadir.Source]string{
	datadir.SourceFlag:     "Option --data",
	datadir.SourceEnv:      "Umgebungsvariable " + datadir.EnvVar,
	datadir.SourcePointer:  "gewählter Datenordner",
	datadir.SourcePortable: "portabel (" + datadir.PortableDirName + " neben dem Programm)",
}

// Reasons for an unreachable data directory.
const (
	reasonNotExist        = "Ordner existiert nicht (Laufwerk nicht verbunden?)"
	reasonNotDir          = "ist kein Ordner"
	reasonPointerEmpty    = "die Zeigerdatei ist leer"
	reasonPointerRelative = "die Zeigerdatei enthält keinen absoluten Pfad"
	reasonPermission      = "keine Berechtigung"
)
