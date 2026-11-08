# RepoShelf – Status

> **Das ist die eine Stelle, an der steht, was offen, in Arbeit und erledigt ist.**
> Regeln zur Pflege: `AGENTS.md`, Abschnitt „Arbeitsablauf“.
> IDs werden **nie** neu vergeben oder umnummeriert; Zeilen werden **nie**
> gelöscht, nur im Status geändert.

**Statuswerte:** `offen` · `in Arbeit` · `Review` · `erledigt` · `blockiert` · `zurückgestellt`
**Wer:** `Dennis` · `Claude Code` · `Codex` · `Claude` (Chat)

---

## Aktueller Stand

- **Phase:** 0 – Gerüst (begonnen)
- **Letzte Änderung:** 2026-11-08 · T-0.3 umgesetzt (`repo.yaml`-Schema), im Review (Branch `t0.3-repo-schema`)
- **Nächster Schritt:** `t0.3-repo-schema` prüfen und mergen; danach T-0.5 (atomares Schreiben, Pfadprüfung) – Voraussetzung für das Speichern von `repo.yaml` und für T-0.12

## Übergabe-Notiz

> Wird am Ende **jeder** Sitzung überschrieben (Agent oder Mensch). Kurz: was
> wurde gemacht, was ist halb fertig, worauf muss der Nächste achten.

**2026-11-08 · Claude Code:** T-0.3 auf Branch `t0.3-repo-schema`
(`6cb6be0`), noch **nicht** gemergt/gepusht.
- `docs/SCHEMA.md` ist jetzt verbindlich (Schema 1); Konzept 5.2 verweist
  darauf. Ein Test prüft, dass das Beispiel dort gültig und kanonisch ist.
- `internal/repometa`: `Parse`/`Load`/`Marshal`. Speichern führt in den
  gelesenen YAML-Baum zusammen (`merge.go`, gesteuert über die Go-Typen):
  unbekannte Schlüssel auf jeder Ebene, Kommentare, Reihenfolge und
  `[a, b]`-Schreibweise bleiben. Listeneinträge werden über `identityKeys`
  wiedererkannt (bisher nur `relations`: `type`+`target`) – **neue Listen
  aus Strukturen dort eintragen**, sonst gehen deren unbekannte Schlüssel
  verloren.
- Migrationsgerüst auf dem YAML-Baum (`migrate.go`), Fixtures
  `testdata/schema1-hand.yaml` und `schema1-minimal.yaml` (nie ändern).
- **Festlegungen, die Dennis prüfen sollte** (in SCHEMA.md dokumentiert, im
  Konzept offen gelassen): `status_override` = `ok|degraded|gone|archived`;
  Beziehungs-Zustand zusätzlich `rejected` (für „abgelehnte Vorschläge
  merken“); Mindestintervall `5m`; erlaubte URL-Schemata
  `https|http|ssh|git` (kein `file:`); `releases_keep: 0` = nur
  angeheftete; Dauer-Syntax `30d`/`1d12h` ohne Wochen.
**Achtung:** `Marshal` liefert Bytes; das Schreiben auf die Platte folgt
mit T-0.5 (atomar). Segmentprüfung in `validate.go` ist die Grundprüfung;
reservierte Namen (Windows) gehören zu T-0.5. ULID-Erzeugung fehlt noch
(wird beim Hinzufügen gebraucht, T-1.8/T-1.11).

---

## Offene Fragen (entscheidet Dennis)

Agenten dürfen Vorschläge ergänzen, aber nicht selbst entscheiden. Nach der
Entscheidung: Status `entschieden`, Eintrag in `docs/ENTSCHEIDUNGEN.md`, Verweis hier.

| ID | Frage | Vorschlag | Status | Entscheidung |
|---|---|---|---|---|
| F-001 | Wird das GitHub-Repo öffentlich oder privat? (Bei öffentlich: Konzept/Design wie bei Fero/Fundus2 in `Dokumentation/` + `.gitignore`?) | Privat starten; `docs/` bleibt im Repo | entschieden | **Privat**, `docs/` und `design/` bleiben im Repo → D-012 |
| F-002 | Repo-Name und Go-Modulpfad | `github.com/Fenron-dev/reposhelf` | entschieden | **`github.com/Fenron-dev/reposhelf`** → D-013 |
| F-003 | Lizenz von RepoShelf | MIT (kompatibel mit Gitea-Übernahmen; GPL nur, falls Forgejo-Code gewollt) | entschieden | **GPL-3.0-or-later** → D-014 |
| F-004 | Wails v2 (stabil) oder v3 (seit 08/2026 Beta)? | In Phase 5 entscheiden; bis dahin keine Wails-Abhängigkeit im Kern | offen | |
| F-005 | Oberfläche: Go-Templates + htmx oder kleines JS-Framework? | htmx (kein Node-Build) | offen | |
| F-006 | Eigene, lokale Issues für eigene Projekte (nicht nur GitHub spiegeln)? | Später; erst spiegeln (T-9.1) | offen | |
| F-007 | Code-Volltextsuche über alle Repos (z. B. zoekt)? | Phase 9 | offen | |
| F-008 | Standard-Port der Weboberfläche | z. B. 7878 (unbelegt prüfen) | entschieden | **7373** (7878 ist Radarr) → D-016 |
| F-009 | Pushes auf eigene Projekte automatisch an GitHub weiterreichen? | Später, optional pro Repo | offen | |
| F-010 | Windows: portables Git mitliefern oder Installation voraussetzen? | Voraussetzen + Prüfung mit Hinweis; mitliefern später | offen | |
| F-011 | Projektordner heißt `Portable RepoShelf ` (Leerzeichen am Ende) – umbenennen? | Ja, auf `Portable RepoShelf` oder `reposhelf` | offen | |
| F-012 | Forgejo-Code übernehmen erlauben (Folge von F-003, bisher durch D-002 verboten)? | Erlauben, da RepoShelf selbst GPL ist | entschieden | **Ja** → D-015 (ersetzt D-002) |

---

## Review-Befunde

Für Prüfungen durch Codex, Claude Code oder Dennis. Befunde werden hier
eingetragen, nicht still im Code geändert (außer der Prüfer hat den Auftrag
dazu). Schwere: `kritisch` · `hoch` · `mittel` · `gering` · `Hinweis`.

| ID | Befund | Fundstelle | Schwere | Status | Gefunden von / Datum | Bezug |
|---|---|---|---|---|---|---|
| – | (noch keine) | | | | | |

---

## Aufgaben

### K – Konzeptphase

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-K.1 | Konzept schreiben | erledigt | Claude | 2026-11-08 | `docs/KONZEPT.md` |
| T-K.2 | UI-Entwurf (5 Ansichten) | erledigt | Claude | 2026-11-08 | `design/` |
| T-K.3 | Projektführung: STATUS, ENTSCHEIDUNGEN, AGENTS, CLAUDE | erledigt | Claude | 2026-11-08 | |

### Phase 0 – Gerüst

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-0.1 | Git-Repo + Go-Modul anlegen; Struktur `cmd/reposhelf`, `internal/…`; `.gitignore`, `LICENSE` (nach F-002/F-003) | erledigt | Claude Code | 2026-11-08 | `132ca84`, gemergt `337d001` (ohne Review, Wunsch Dennis) |
| T-0.2 | Konfiguration (`reposhelf.yaml` + Umgebungsvariablen) und Datenordner-Kette (Konzept 4.4) | erledigt | Claude Code | 2026-11-08 | `304e735`, gemergt `76c3bfb` (ohne Review, Wunsch Dennis) |
| T-0.3 | `repo.yaml`-Schema in `docs/SCHEMA.md` festschreiben; Lesen/Schreiben mit Erhalt unbekannter Felder; Validierung; Tests inkl. Vorversion | Review | Claude Code | 2026-11-08 | Branch `t0.3-repo-schema`, `6cb6be0` |
| T-0.4 | SQLite-Treiber ohne CGO mit FTS5 wählen und begründen; Index-Schema; `reindex` aus Dateien | offen | | | |
| T-0.5 | Atomares Schreiben und Pfad-Sanitizing, mit Tests (Fero-Invarianten) | offen | | | |
| T-0.6 | Gitea-Quellcode sichten: Übernahme-Kandidaten mit Pfad + Commit in `docs/GITEA-UEBERNAHMEN.md`; `THIRD_PARTY.md` anlegen | offen | | | Seit D-015 auch Forgejo-Code übernehmbar |
| T-0.7 | CI (GitHub Actions): `go vet`, `go test`, `golangci-lint`, Cross-Build linux/darwin/windows – **alles auf Linux-Runnern** (Go baut darwin/windows ohne CGO per Cross-Compile); macOS-/Windows-Runner nur manuell oder bei `v*`-Tag (D-012) | offen | | | |
| T-0.8 | Logging- und Fehlerkonzept (strukturiertes Log, Fehlerarten) | offen | | | |
| T-0.9 | SQLite auf exFAT/FAT erkennen → `journal_mode=DELETE` | offen | | | |
| T-0.10 | Prüfen: selbst gehosteter GitHub-Actions-Runner auf Unraid (Docker) für private Repos – kostet keine Actions-Minuten | offen | | | Vorstufe zu Phase 7 |
| T-0.11 | Privates GitHub-Repo `Fenron-dev/reposhelf` anlegen (D-012, D-013), `main` und Branches pushen | erledigt | Claude Code | 2026-11-08 | https://github.com/Fenron-dev/reposhelf |
| T-0.12 | Datenordner einrichten: Zeigerdatei atomar schreiben (nach T-0.5), Grundstruktur anlegen, CLI `reposhelf init <ordner>`; später Einrichtungsdialog | offen | | | aus T-0.2; `datadir.PointerPath` gibt den Ort vor |

### Phase 1 – Spiegel-Kern

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-1.1 | Quelle GitHub: URL-Parser, Metadaten-Abruf, Token-Handling, Rate-Limit-Beachtung | offen | | | |
| T-1.2 | Git-CLI-Wrapper (Version prüfen, Timeouts, Abbruch, Fehlertexte) + Mirror-Klon | offen | | | |
| T-1.3 | Abgleich mit Force-Push-Schutz (Backup-Refs vor verändernden Fetches); Tests mit lokalem Fake-Upstream | offen | | | |
| T-1.4 | Zeitplaner: Intervalle, Verteilung über die Zeit, 30-Tage-Ruhe für archivierte | offen | | | |
| T-1.5 | Upstream-Status und Host-Gesundheit (alle Repos eines Hosts ≥ 24 h) | offen | | | |
| T-1.6 | `upstream.json`-Snapshot mit Historie; Lizenz-Erkennung | offen | | | |
| T-1.7 | Releases und Assets herunterladen (SHA-256, Dubletten) | offen | | | |
| T-1.8 | CLI: `add`, `sync`, `list`, `reindex`, `doctor` | offen | | | |
| T-1.9 | Weboberfläche: Grundlayout, Seitenleiste, Bibliothek (Entwurf `design/quellen/Main.dc.html`) | offen | | | |
| T-1.10 | Weboberfläche: Repo-Detail mit README (goldmark/GFM) | offen | | | |
| T-1.11 | Weboberfläche: Repo hinzufügen | offen | | | |

### Phase 2 – Organisation

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-2.1 | Schlagwörter hierarchisch; global umbenennen/zusammenführen | offen | | | |
| T-2.2 | Sammlungen (manuell), Farbe/Icon | offen | | | |
| T-2.3 | Beziehungen (Edge-Modell) und Backlinks | offen | | | |
| T-2.4 | Smarte Sammlungen – SmartFilter-Schema aus MediaShelf/OracleVault übernehmen | offen | | | |
| T-2.5 | Beziehungs-Vorschläge: README-Links, Abhängigkeitsdateien, Fork-Herkunft | offen | | | |
| T-2.6 | Notizen mit Wiki-Links `[[…]]` | offen | | | |
| T-2.7 | Volltextsuche (FTS5) und Befehlspalette ⌘K | offen | | | |

### Phase 3 – Aufbewahrung

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-3.1 | Regel-Engine auf drei Ebenen (global → Sammlung → Repo) | offen | | | |
| T-3.2 | Anheften | offen | | | |
| T-3.3 | Lösch-Prüfung beim Start (Entwurf `design/quellen/Cleanup.dc.html`) | offen | | | |
| T-3.4 | Papierkorb: Manifest, Wiederherstellen, endgültiges Löschen nach Bestätigung | offen | | | |
| T-3.5 | Vorschau „Was würde die nächste Bereinigung entfernen?“ | offen | | | |

### Phase 4 – Bereitstellen für Agenten

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-4.1 | Git Smart-HTTP lesend (clone/fetch) | offen | | | |
| T-4.2 | `git push` für `own`/`fork` | offen | | | |
| T-4.3 | Tokens mit Rechten (lesen/schreiben/bauen) | offen | | | |
| T-4.4 | MCP-Server stdio mit Lesewerkzeugen | offen | | | |
| T-4.5 | MCP Streamable HTTP | offen | | | |
| T-4.6 | MCP-Schreibwerkzeuge und Aktionsprotokoll | offen | | | |

### Phase 5 – Verpackung

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-5.1 | Dockerfile + Image (mit `git`, `git-lfs`) | offen | | | Image setzt `REPOSHELF_DATA=/data` und `REPOSHELF_LISTEN=:7373` (T-0.2, D-016) |
| T-5.2 | Unraid-Template | offen | | | |
| T-5.3 | Desktop-Hülle (Wails; vorher F-004) | offen | | | |
| T-5.4 | Portabler Modus, macOS-Bundle-Pfade, Linux `noexec` | offen | | | |
| T-5.5 | Release-Workflow (macOS DMG, Windows, Linux, Docker) | offen | | | |

### Phase 6 – Baubar halten

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-6.1 | Kapsel-Manifest und Ablage | offen | | | |
| T-6.2 | Ökosysteme Go und Rust | offen | | | |
| T-6.3 | Ökosystem Dart/Flutter | offen | | | |
| T-6.4 | Ökosysteme Node und Python; Docker-Basis-Images | offen | | | |
| T-6.5 | Inhaltsadressierter Dedup-Cache | offen | | | |
| T-6.6 | Kapsel-Test (Offline-Build) | offen | | | |

### Phase 7 – Builds & Runner

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-7.1 | Runner-Rolle und Fähigkeiten-Meldung | offen | | | |
| T-7.2 | Ausführung von GitHub-Workflows mit `act` | offen | | | |
| T-7.3 | Eigene Build-Definition `reposhelf-build.yaml` | offen | | | |
| T-7.4 | GitHub Actions als externer Runner, Artefakte ins Archiv | offen | | | |
| T-7.5 | Speicherprüfung und Bestätigungspflicht für schwache Runner | offen | | | |
| T-7.6 | MCP-Werkzeuge `start_build` / `get_build` | offen | | | |

### Phase 8 – Abgleich zwischen Instanzen

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-8.1 | Kopplung (Code, Token, Fingerprint) | offen | | | |
| T-8.2 | Selektiver Abgleich (Stufen) | offen | | | |
| T-8.3 | Feldweise Konflikterkennung und -anzeige | offen | | | |

### Phase 9 – Ausbau

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-9.1 | GitHub-Issues spiegeln | offen | | | |
| T-9.2 | Weitere Quellen: GitLab, Codeberg/Gitea/Forgejo, Git-URL, ZIP | offen | | | |
| T-9.3 | Themes (hell, Akzentfarbe) | offen | | | |
| T-9.4 | Graph-Ansicht Beziehungen | offen | | | |
| T-9.5 | Code-Volltextsuche (nach F-007) | offen | | | |
| T-9.6 | Massenimport (eigene / gesternte GitHub-Repos) | offen | | | |

---

## Verlauf

Neueste oben. Eine Zeile pro Sitzung.

- 2026-11-08 · Claude Code · T-0.3 `repo.yaml`-Schema (docs/SCHEMA.md, internal/repometa) → Review
- 2026-11-08 · Claude Code · F-008 entschieden (7373, D-016); T-0.2 umgesetzt, gemergt und gepusht (erledigt); T-0.12 neu
- 2026-11-08 · Claude Code · T-0.1 gemergt (erledigt); T-0.11 privates GitHub-Repo angelegt und gepusht (erledigt)
- 2026-11-08 · Claude Code · F-002, F-003, F-012 entschieden (D-013–D-015); Git-Repo angelegt; T-0.1 umgesetzt → Review; T-0.11 neu
- 2026-11-08 · Claude · F-001 entschieden (privat, D-012); T-0.7 angepasst, T-0.10 neu
- 2026-11-08 · Claude · Konzept, UI-Entwurf, Projektführung angelegt (T-K.1–T-K.3)
