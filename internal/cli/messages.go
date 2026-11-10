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

Einrichten:
  init [ordner]            Datenordner anlegen (ohne Angabe: portabel neben dem Programm)
  config                   Datenordner und wirksame Einstellungen anzeigen
  doctor                   Installation und Datenordner prüfen

Repos:
  add <link>…              Repo per Link sichern
       --kind mirror|own   --mode follow|once|manual   --interval 6h
       --tags a,b   --collection x   --buildable   --no-releases   --issues   --wiki
       --actions           GitHub-Actions-Artefakte mitsichern (neueste Läufe, Anzahl: retention.actions_keep)
  create <name>            Eigenes Projekt nur in RepoShelf anlegen (--owner, --description)
  fork <repo>              Beschreibbare Kopie eines Spiegels anlegen (--name)
  sync <repo>… | --all | --due   Abgleichen (mit Sicherung vor Force-Pushes)
  list                     Repos auflisten (--tag x/* --kind --status --collection --search --json)
  show <repo>              Einzelheiten (--json)
  tags [<repo> a,b]        Schlagwörter anzeigen bzw. setzen
  rename-tag <alt> <neu>   Schlagwort überall umbenennen/zusammenführen
  import                   Alle eigenen (--starred: gesternten) Repos übernehmen (--host, --dry-run)
  import-zip <datei.zip>   Eigenes Projekt aus einem ZIP anlegen (mit .git: ganze Geschichte)
  grep <text>              Code aller Repos durchsuchen (--regex -i --tag --repo)
  reindex                  Index aus den Dateien neu aufbauen
  issue list|new|comment|close|reopen <repo> [L3]
                           Lokale Issues (--title, --body, --body - liest von der
                           Eingabe, --labels a,b, --all)
  host-token list | check | set | remove [<host>]
                           Zugänge zu Git-Hosts (Standard github.com); set liest den
                           Token von der Eingabe und legt ihn im Schlüsselbund ab

Aufbewahrung:
  cleanup                  Zeigen, was die Regeln entfernen würden
  cleanup --to-trash ID,…  Einträge in den Papierkorb verschieben (--keep ID,… behalten)
  trash                    Papierkorb anzeigen
  trash restore <id>…      Wiederherstellen
  trash purge <id>… --yes  Endgültig löschen

Bereitstellen:
  serve                    Weboberfläche, API, Git-HTTP und MCP starten (--listen)
  mcp                      MCP-Server über stdio (für Claude, Codex, LM Studio)
  token create|list|revoke Zugangs-Tokens für Git, API und MCP verwalten

Sonstiges:
  version   Version anzeigen
  help      Diese Hilfe anzeigen

Optionen:
  --data <ordner>   Datenordner festlegen. Ohne Angabe gilt der Reihe nach:
                    Umgebungsvariable ` + datadir.EnvVar + `, der gewählte Datenordner,
                    der Ordner ` + datadir.PortableDirName + ` neben dem Programm.

Repo-Angaben: Kennung (ULID) oder host/owner/name.

Umgebungsvariablen:
  ` + datadir.EnvVar + `      Datenordner (Docker: /data)
  REPOSHELF_GITHUB_TOKEN  Token für GitHub (auch GITHUB_TOKEN; *_FILE für Docker-Secrets)
  REPOSHELF_TOKEN_<HOST>  Token für andere Hosts, z. B. REPOSHELF_TOKEN_CODEBERG_ORG
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

// Texts of the repository commands.
const (
	msgNoGit = `Git wurde nicht gefunden oder ist zu alt (%v).
Installiere Git (https://git-scm.com) oder lege ein portables Git in den Ordner „git“ neben RepoShelf.
`
	msgInitNeedsDir    = "Bitte einen Ordner angeben: reposhelf init <ordner>\n"
	msgInitDone        = "Datenordner eingerichtet: %s\n"
	msgInitFAT         = "Hinweis: Dateisystem %s – der Index arbeitet ohne WAL (sicher auf USB-Sticks).\n"
	msgInitPortable    = "Portabler Betrieb: RepoShelf findet den Ordner neben dem Programm automatisch.\n"
	msgInitPointer     = "Als gewählter Datenordner gespeichert in %s\n"
	msgInitNoPointer   = "Kein Zeiger gespeichert. Starte RepoShelf mit --data %s oder setze " + datadir.EnvVar + ".\n"
	msgAdding          = "Sichere %s …\n"
	msgAdded           = "✓ %s – %s, %s\n"
	msgCreated         = "✓ %s angelegt. Git-Adresse (bei laufendem Server): <server>%s\n"
	msgSyncWhat        = "Welche Repos? reposhelf sync <repo>… oder --all bzw. --due\n"
	msgSynced          = "%s: %d neu, %d aktualisiert, %d umgeschrieben – %s\n"
	msgBackedUp        = "  Geschichte wurde beim Original umgeschrieben – alter Stand gesichert unter %s\n"
	msgDeletedUpstream = "  %d Branches/Versionen beim Original gelöscht – hier bleiben sie erhalten\n"
	msgListEmpty       = "Keine Repos gefunden. Mit „reposhelf add <link>“ eines hinzufügen.\n"
	msgTagRenamed      = "„%s“ → „%s“ in %d Repos.\n"
	msgReindexed       = "Index neu aufgebaut: %d Repos.\n"
	msgTrashed         = "%d Einträge in den Papierkorb verschoben.\n"
	msgCleanupNone     = "Nichts zu bereinigen.\n"
	msgCleanupHint     = "\nIn den Papierkorb: reposhelf cleanup --to-trash ID,…   Behalten: reposhelf cleanup --keep ID,…\n"
	msgTrashEmpty      = "Der Papierkorb ist leer.\n"
	msgPurgeConfirm    = "Endgültiges Löschen kann nicht rückgängig gemacht werden. Mit --yes bestätigen.\n"
	msgImported        = "%d übernommen, %d schon vorhanden, %d fehlgeschlagen. Abgleich läuft im Hintergrund, wenn der Server läuft – sonst „reposhelf sync --due“.\n"
)

var statusLabels = map[string]string{
	"ok": "erreichbar", "degraded": "vorübergehend gestört", "gone": "Upstream weg",
	"archived": "archiviert", "local": "nur lokal", "pending": "noch nicht abgeglichen",
}

func statusLabel(s string) string {
	if l, ok := statusLabels[s]; ok {
		return l
	}
	return s
}

var kindLabels = map[string]string{"own": "Eigen", "mirror": "Spiegel", "fork": "Fork"}

func kindLabel(k string) string {
	if l, ok := kindLabels[k]; ok {
		return l
	}
	return k
}

// Texts of serve, mcp and token.
const (
	msgServing      = "RepoShelf läuft: http://%s  (Daten: %s)\nBeenden mit Strg+C.\n"
	msgTokenCreated = "Token „%s“ (%s) angelegt. Es wird nur jetzt angezeigt:\n\n  %s\n\nGit: als Passwort verwenden · API/MCP: Authorization: Bearer <Token>\n"
)
