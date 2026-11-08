# RepoShelf – Status

> **Das ist die eine Stelle, an der steht, was offen, in Arbeit und erledigt ist.**
> Regeln zur Pflege: `AGENTS.md`, Abschnitt „Arbeitsablauf“.
> IDs werden **nie** neu vergeben oder umnummeriert; Zeilen werden **nie**
> gelöscht, nur im Status geändert.

**Statuswerte:** `offen` · `in Arbeit` · `Review` · `erledigt` · `blockiert` · `zurückgestellt`
**Wer:** `Dennis` · `Claude Code` · `Codex` · `Claude` (Chat)

---

## Aktueller Stand

- **Phase:** 0–9 umgesetzt (außer T-9.5, wartet auf F-007)
- **Letzte Änderung:** 2026-11-08 · Builds/Kapseln (6/7), Abgleich zwischen Instanzen (8), ZIP-Import, Größenbegrenzung
- **Nächster Schritt:** Dennis testet Release `v0.1.1` (CLI-Pakete, DMG, Windows-ZIP, Docker-Image `ghcr.io/fenron-dev/reposhelf:0.1.1`); F-007 und F-014 entscheiden

## Übergabe-Notiz

> Wird am Ende **jeder** Sitzung überschrieben (Agent oder Mensch). Kurz: was
> wurde gemacht, was ist halb fertig, worauf muss der Nächste achten.

**2026-11-08 · Claude Code:** Projekt nach Konzept umgesetzt (Auftrag „fertigstellen“).
Alles auf `main`, CI (Linux: gofmt, vet, Tests mit Race-Detector, golangci-lint,
Cross-Builds) grün.

**Zum Testen (lokal, ohne Docker):**
```
go build -o bin/reposhelf ./cmd/reposhelf
./bin/reposhelf init ~/RepoShelf-Test
./bin/reposhelf add https://github.com/octocat/Hello-World
./bin/reposhelf serve      # http://127.0.0.1:7373
```
Anleitung für alles Weitere: `docs/BETRIEB.md`.

**Nur in CI/auf Zielsystemen prüfbar (lokal nicht erlaubt):** Docker-Image,
Desktop-Apps (Wails v2: bisher nur Typprüfung, nie gestartet), echte Builds in
Containern und mit `act`, GitHub-Actions-Dispatch. Erstes Release: Tag `v0.1.0`
pushen oder `Release`-Workflow manuell mit „Desktop-Apps bauen“ starten.

**Offene Fragen:** keine (F-007 → D-021, F-014 → D-022).

**Achtung:**
- Release-Dateien können groß werden: global `retention.max_asset_mb` setzen
  (T-1.13), der Hinzufügen-Dialog zeigt die erwartete Größe.
- Die Vorschau der Claude-Desktop-App startet Programme aus `~/Documents` nicht
  (macOS-Freigabe); Server zum Ansehen im Terminal starten.

---

## Offene Fragen (entscheidet Dennis)

Agenten dürfen Vorschläge ergänzen, aber nicht selbst entscheiden. Nach der
Entscheidung: Status `entschieden`, Eintrag in `docs/ENTSCHEIDUNGEN.md`, Verweis hier.

| ID | Frage | Vorschlag | Status | Entscheidung |
|---|---|---|---|---|
| F-001 | Wird das GitHub-Repo öffentlich oder privat? (Bei öffentlich: Konzept/Design wie bei Fero/Fundus2 in `Dokumentation/` + `.gitignore`?) | Privat starten; `docs/` bleibt im Repo | entschieden | **Privat**, `docs/` und `design/` bleiben im Repo → D-012 |
| F-002 | Repo-Name und Go-Modulpfad | `github.com/Fenron-dev/reposhelf` | entschieden | **`github.com/Fenron-dev/reposhelf`** → D-013 |
| F-003 | Lizenz von RepoShelf | MIT (kompatibel mit Gitea-Übernahmen; GPL nur, falls Forgejo-Code gewollt) | entschieden | **GPL-3.0-or-later** → D-014 |
| F-004 | Wails v2 (stabil) oder v3 (seit 08/2026 Beta)? | In Phase 5 entscheiden; bis dahin keine Wails-Abhängigkeit im Kern | entschieden | **Wails v2** → D-019 |
| F-005 | Oberfläche: Go-Templates + htmx oder kleines JS-Framework? | htmx (kein Node-Build) | entschieden | **Go-Templates + htmx** → D-017 |
| F-006 | Eigene, lokale Issues für eigene Projekte (nicht nur GitHub spiegeln)? | Später; erst spiegeln (T-9.1) | offen | |
| F-007 | Code-Volltextsuche über alle Repos (z. B. zoekt)? | Phase 9 | entschieden | **git grep, ohne Index** → D-021 |
| F-008 | Standard-Port der Weboberfläche | z. B. 7878 (unbelegt prüfen) | entschieden | **7373** (7878 ist Radarr) → D-016 |
| F-009 | Pushes auf eigene Projekte automatisch an GitHub weiterreichen? | Später, optional pro Repo | offen | |
| F-010 | Windows: portables Git mitliefern oder Installation voraussetzen? | Voraussetzen + Prüfung mit Hinweis; mitliefern später | entschieden | **Portables Git mitliefern** → D-020 |
| F-011 | Projektordner heißt `Portable RepoShelf ` (Leerzeichen am Ende) – umbenennen? | Ja, auf `Portable RepoShelf` oder `reposhelf` | offen | |
| F-012 | Forgejo-Code übernehmen erlauben (Folge von F-003, bisher durch D-002 verboten)? | Erlauben, da RepoShelf selbst GPL ist | entschieden | **Ja** → D-015 (ersetzt D-002) |
| F-013 | Wie wird ein Host mit Port (`gitea.local:3000`, z. B. Gitea auf Unraid) im Ordnerpfad abgebildet? `:` ist unter Windows im Dateinamen verboten; bis zur Entscheidung lehnt die Prüfung solche Hosts ab | `source.host` bleibt `gitea.local:3000`, Ordner heißt `gitea.local+3000` (`+` kommt in Hostnamen nie vor); IPv6-Adressen ebenso ohne `[]` und `:` abbilden | entschieden | **Ordner ohne Port** (`gitea.local`), Doppelte werden gemeldet → D-018 |
| F-014 | Der Instanzschlüssel für den Abgleich (Ed25519, `.reposhelf/instance.key`, Rechte 0600) liegt im Datenordner. Konzept 4.2 sagt „Geheimnisse nie im Datenordner im Klartext“. So lassen? | So lassen: Er ist ein selbst erzeugter Maschinenschlüssel wie ein SSH-Hostkey, verlässt die Instanz nie und macht den portablen Betrieb (USB, Docker-Volume) erst möglich. Alternative: auf dem Desktop im Schlüsselbund, in Docker als Secret-Datei | entschieden | **So lassen** (Datei, 0600) → D-022 |

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
| T-0.3 | `repo.yaml`-Schema in `docs/SCHEMA.md` festschreiben; Lesen/Schreiben mit Erhalt unbekannter Felder; Validierung; Tests inkl. Vorversion | erledigt | Claude Code | 2026-11-08 | `6cb6be0`, gemergt `7df78c3` (ohne Review, Wunsch Dennis; Festlegungen bestätigt) |
| T-0.4 | SQLite-Treiber ohne CGO mit FTS5 wählen und begründen; Index-Schema; `reindex` aus Dateien | erledigt | Claude Code | 2026-11-08 | `64cb17a` – modernc.org/sqlite (rein Go, FTS5); Index wird bei Schemawechsel neu aufgebaut |
| T-0.5 | Atomares Schreiben und Pfad-Sanitizing, mit Tests (Fero-Invarianten) | erledigt | Claude Code | 2026-11-08 | `f5619cb`, gemergt `fc8b09f` (ohne Review, Wunsch Dennis) |
| T-0.6 | Gitea-Quellcode sichten: Übernahme-Kandidaten mit Pfad + Commit in `docs/GITEA-UEBERNAHMEN.md`; `THIRD_PARTY.md` anlegen | erledigt | Claude Code | 2026-11-08 | `docs/GITEA-UEBERNAHMEN.md` – Stand go-gitea/gitea `8cb6fb2`; bisher kein Code übernommen (Gründe dort); Seit D-015 auch Forgejo-Code übernehmbar |
| T-0.7 | CI (GitHub Actions): `go vet`, `go test`, `golangci-lint`, Cross-Build linux/darwin/windows – **alles auf Linux-Runnern** (Go baut darwin/windows ohne CGO per Cross-Compile); macOS-/Windows-Runner nur manuell oder bei `v*`-Tag (D-012) | erledigt | Claude Code | 2026-11-08 | `.github/workflows/ci.yml`, `.golangci.yml`; erster Lauf grün nach Lint-Fixes |
| T-0.8 | Logging- und Fehlerkonzept (strukturiertes Log, Fehlerarten) | erledigt | Claude Code | 2026-11-08 | `64cb17a` – `internal/logx` (slog, Rotation, Token-Schwärzung); Fehlerarten als Sentinel-Fehler |
| T-0.9 | SQLite auf exFAT/FAT erkennen → `journal_mode=DELETE` | erledigt | Claude Code | 2026-11-08 | `64cb17a` – `internal/fsinfo` |
| T-0.10 | Prüfen: selbst gehosteter GitHub-Actions-Runner auf Unraid (Docker) für private Repos – kostet keine Actions-Minuten | erledigt | Claude Code | 2026-11-08 | `docs/RUNNER-UNRAID.md` – machbar, nur für eigene private Repos; Vorstufe zu Phase 7 |
| T-0.11 | Privates GitHub-Repo `Fenron-dev/reposhelf` anlegen (D-012, D-013), `main` und Branches pushen | erledigt | Claude Code | 2026-11-08 | https://github.com/Fenron-dev/reposhelf |
| T-0.12 | Datenordner einrichten: Zeigerdatei atomar schreiben (nach T-0.5), Grundstruktur anlegen, CLI `reposhelf init <ordner>`; später Einrichtungsdialog | erledigt | Claude Code | 2026-11-08 | `fad38bc` – `reposhelf init [ordner]`, Zeigerdatei atomar; aus T-0.2; `datadir.PointerPath` gibt den Ort vor |
| T-0.13 | Liegengebliebene Zwischendateien (`.reposhelf-tmp-*`, nach Absturz) beim Start bzw. in `doctor` erkennen und aufräumen – nur reguläre Dateien mit diesem Präfix | erledigt | Claude Code | 2026-11-08 | `64cb17a`, `fad38bc` – beim Start von `serve` und in `doctor`; aus T-0.5; `atomicfile.IsTemp` |

### Phase 1 – Spiegel-Kern

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-1.1 | Quelle GitHub: URL-Parser, Metadaten-Abruf, Token-Handling, Rate-Limit-Beachtung | erledigt | Claude Code | 2026-11-08 | `1ac3cd1` – auch Gitea/Forgejo/Codeberg und GitLab; ETag-Cache |
| T-1.2 | Git-CLI-Wrapper (Version prüfen, Timeouts, Abbruch, Fehlertexte) + Mirror-Klon | erledigt | Claude Code | 2026-11-08 | `b6ff029` – Schutz gegen Optionen in Namen (`--`, `--end-of-options`); Namen aus dem Netz dürfen mit `-` beginnen: Pfade/URLs nur absolut oder nach `--` übergeben (aus T-0.5) |
| T-1.3 | Abgleich mit Force-Push-Schutz (Backup-Refs vor verändernden Fetches); Tests mit lokalem Fake-Upstream | erledigt | Claude Code | 2026-11-08 | `b6ff029` – erst Fetch ohne Zwang, dann Sicherung unter `refs/reposhelf/backup/<zeit>/` und gezielter Force-Fetch |
| T-1.4 | Zeitplaner: Intervalle, Verteilung über die Zeit, 30-Tage-Ruhe für archivierte | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-1.5 | Upstream-Status und Host-Gesundheit (alle Repos eines Hosts ≥ 24 h) | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-1.6 | `upstream.json`-Snapshot mit Historie; Lizenz-Erkennung | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` – Lizenz aus API und LICENSE-Datei |
| T-1.7 | Releases und Assets herunterladen (SHA-256, Dubletten) | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` – Tags mit `/` als `%2F` abgelegt (umkehrbar); Git-Tags dürfen `/` enthalten (`release/1.0`), `safepath` lehnt das ab → Abbildung auf Ordnernamen festlegen (aus T-0.5) |
| T-1.8 | CLI: `add`, `sync`, `list`, `reindex`, `doctor` | erledigt | Claude Code | 2026-11-08 | `fad38bc` – zusätzlich create, fork, show, tags, rename-tag, cleanup, trash, import |
| T-1.9 | Weboberfläche: Grundlayout, Seitenleiste, Bibliothek (Entwurf `design/quellen/Main.dc.html`) | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-1.10 | Weboberfläche: Repo-Detail mit README (goldmark/GFM) | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-1.11 | Weboberfläche: Repo hinzufügen | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-1.12 | Namenskollisionen erkennen: Repos, deren Pfad sich nur in Groß-/Kleinschreibung oder Unicode-Normalisierung (NFC/NFD) unterscheidet, landen auf macOS/Windows im selben Ordner – beim Hinzufügen ablehnen mit Meldung | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` – Index-Schlüssel ohne Groß-/Kleinschreibung, NFC; aus T-0.5 |
| T-1.13 | Release-Dateien begrenzen: Höchstgröße je Datei/Release einstellbar, erwartete Downloadgröße im Hinzufügen-Dialog anzeigen | erledigt | Claude Code | 2026-11-08 | `retention.max_asset_mb`; Größenschätzung im Hinzufügen-Dialog; aus Praxistest: zwei Repos luden mit „neueste 5 Releases“ 1,4 GB |

### Phase 2 – Organisation

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-2.1 | Schlagwörter hierarchisch; global umbenennen/zusammenführen | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-2.2 | Sammlungen (manuell), Farbe/Icon | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-2.3 | Beziehungen (Edge-Modell) und Backlinks | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-2.4 | Smarte Sammlungen – SmartFilter-Schema aus MediaShelf/OracleVault übernehmen | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` – Format `{logic, rules}` aus OracleVault/MediaShelf |
| T-2.5 | Beziehungs-Vorschläge: README-Links, Abhängigkeitsdateien, Fork-Herkunft | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-2.6 | Notizen mit Wiki-Links `[[…]]` | erledigt | Claude Code | 2026-11-08 | `6f0ca0a`; freie Notizen unter `notes/` mit Rückverweisen ergänzt |
| T-2.7 | Volltextsuche (FTS5) und Befehlspalette ⌘K | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |

### Phase 3 – Aufbewahrung

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-3.1 | Regel-Engine auf drei Ebenen (global → Sammlung → Repo) | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-3.2 | Anheften | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-3.3 | Lösch-Prüfung beim Start (Entwurf `design/quellen/Cleanup.dc.html`) | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-3.4 | Papierkorb: Manifest, Wiederherstellen, endgültiges Löschen nach Bestätigung | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |
| T-3.5 | Vorschau „Was würde die nächste Bereinigung entfernen?“ | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` |

### Phase 4 – Bereitstellen für Agenten

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-4.1 | Git Smart-HTTP lesend (clone/fetch) | erledigt | Claude Code | 2026-11-08 | `98f3ade` |
| T-4.2 | `git push` für `own`/`fork` | erledigt | Claude Code | 2026-11-08 | `98f3ade` |
| T-4.3 | Tokens mit Rechten (lesen/schreiben/bauen) | erledigt | Claude Code | 2026-11-08 | `98f3ade` |
| T-4.4 | MCP-Server stdio mit Lesewerkzeugen | erledigt | Claude Code | 2026-11-08 | `98f3ade` |
| T-4.5 | MCP Streamable HTTP | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-4.6 | MCP-Schreibwerkzeuge und Aktionsprotokoll | erledigt | Claude Code | 2026-11-08 | `98f3ade` |

### Phase 5 – Verpackung

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-5.1 | Dockerfile + Image (mit `git`, `git-lfs`) | erledigt | Claude Code | 2026-11-08 | `Dockerfile` (git, git-lfs, docker-cli, act mit Prüfsumme); Bau nur in CI; Image setzt `REPOSHELF_DATA=/data` und `REPOSHELF_LISTEN=:7373` (T-0.2, D-016) |
| T-5.2 | Unraid-Template | erledigt | Claude Code | 2026-11-08 | `deploy/unraid/reposhelf.xml` |
| T-5.3 | Desktop-Hülle (Wails; vorher F-004) | erledigt | Claude Code | 2026-11-08 | `cmd/reposhelf-desktop` (Wails v2, D-019) mit Einrichtungsdialog; nur in CI gebaut – **noch nicht auf einem Mac gestartet** |
| T-5.4 | Portabler Modus, macOS-Bundle-Pfade, Linux `noexec` | erledigt | Claude Code | 2026-11-08 | portabler Ordner neben Programm bzw. `.app` (T-0.2), MinGit für Windows (D-020); Linux `noexec` unkritisch, weil im Datenordner nichts ausgeführt wird |
| T-5.5 | Release-Workflow (macOS DMG, Windows, Linux, Docker) | erledigt | Claude Code | 2026-11-08 | `.github/workflows/release.yml` (CLI-Archive, ghcr.io-Image, DMG, Windows-ZIP mit MinGit) |

### Phase 6 – Baubar halten

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-6.1 | Kapsel-Manifest und Ablage | erledigt | Claude Code | 2026-11-08 | `internal/artifact`, `internal/runner/capsule.go` |
| T-6.2 | Ökosysteme Go und Rust | erledigt | Claude Code | 2026-11-08 | Go (`go mod vendor`), Rust (`cargo vendor`) |
| T-6.3 | Ökosystem Dart/Flutter | erledigt | Claude Code | 2026-11-08 | Dart/Flutter (PUB_CACHE in der Kapsel) |
| T-6.4 | Ökosysteme Node und Python; Docker-Basis-Images | erledigt | Claude Code | 2026-11-08 | Node (npm-Cache), Python (`pip download`), Docker (`docker save`) |
| T-6.5 | Inhaltsadressierter Dedup-Cache | erledigt | Claude Code | 2026-11-08 | `capsules/blobs/sha256/…`; ungenutzte Dateien in der Lösch-Prüfung |
| T-6.6 | Kapsel-Test (Offline-Build) | erledigt | Claude Code | 2026-11-08 | Offline-Build mit `--network none`; Ergebnis im Manifest |

### Phase 7 – Builds & Runner

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-7.1 | Runner-Rolle und Fähigkeiten-Meldung | erledigt | Claude Code | 2026-11-08 | Runner-Liste mit Fähigkeiten, Docker- und Speicherprüfung |
| T-7.2 | Ausführung von GitHub-Workflows mit `act` | erledigt | Claude Code | 2026-11-08 | `act` für Linux-Jobs |
| T-7.3 | Eigene Build-Definition `reposhelf-build.yaml` | erledigt | Claude Code | 2026-11-08 | `reposhelf-build.yaml` (siehe BETRIEB.md) |
| T-7.4 | GitHub Actions als externer Runner, Artefakte ins Archiv | erledigt | Claude Code | 2026-11-08 | workflow_dispatch, Lauf verfolgen, Artefakte ins Archiv |
| T-7.5 | Speicherprüfung und Bestätigungspflicht für schwache Runner | erledigt | Claude Code | 2026-11-08 | Bestätigungspflicht, Mindestspeicher, Ressourcengrenzen |
| T-7.6 | MCP-Werkzeuge `start_build` / `get_build` | erledigt | Claude Code | 2026-11-08 | MCP `start_build`/`get_build` |

### Phase 8 – Abgleich zwischen Instanzen

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-8.1 | Kopplung (Code, Token, Fingerprint) | erledigt | Claude Code | 2026-11-08 | Ed25519-Schlüssel, Kopplungscode, Fingerprint, signierte Anfragen; siehe F-014 |
| T-8.2 | Selektiver Abgleich (Stufen) | erledigt | Claude Code | 2026-11-08 | Auswahl alle/Sammlung/Repo × Stufe meta/code/releases/capsules; manuell oder Intervall |
| T-8.3 | Feldweise Konflikterkennung und -anzeige | erledigt | Claude Code | 2026-11-08 | Drei-Wege-Vergleich je Feld, Konflikte in der Oberfläche |

### Phase 9 – Ausbau

| ID | Aufgabe | Status | Wer | Datum | Notiz / Commit |
|---|---|---|---|---|---|
| T-9.1 | GitHub-Issues spiegeln | erledigt | Claude Code | 2026-11-08 | `6f0ca0a` – Markdown mit YAML-Frontmatter unter `meta/…/issues/` |
| T-9.2 | Weitere Quellen: GitLab, Codeberg/Gitea/Forgejo, Git-URL, ZIP | erledigt | Claude Code | 2026-11-08 | GitLab, Gitea/Forgejo/Codeberg, beliebige Git-URL, ZIP-Import (mit .git: ganze Geschichte); GitLab, Gitea/Forgejo/Codeberg, beliebige Git-URL fertig (`1ac3cd1`); ZIP-Import fehlt |
| T-9.3 | Themes (hell, Akzentfarbe) | erledigt | Claude Code | 2026-11-08 | `9dc73ff` – hell/dunkel |
| T-9.4 | Graph-Ansicht Beziehungen | erledigt | Claude Code | 2026-11-08 | `9dc73ff` |
| T-9.5 | Code-Volltextsuche (nach F-007) | erledigt | Claude Code | 2026-11-08 | D-021 – `git grep` in Oberfläche (`/code`), CLI (`reposhelf grep`) und MCP (`search_code`) |
| T-9.6 | Massenimport (eigene / gesternte GitHub-Repos) | erledigt | Claude Code | 2026-11-08 | `fad38bc` – `reposhelf import [--starred]` |

---

## Verlauf

Neueste oben. Eine Zeile pro Sitzung.

- 2026-11-08 · Claude Code · F-007/F-014 entschieden (D-021, D-022); Code-Suche (T-9.5); Prüflauf `smoke.yml` fand Fehler: Docker-Image und MinGit haben kein `git http-backend` → Smart-HTTP jetzt eigen (upload-pack/receive-pack); Release `v0.1.3`
- 2026-11-08 · Claude Code · Release `v0.1.1` gebaut (alle Jobs grün). `v0.1.0` schlug fehl (Wails-Bindings, Artefakt-Kontingent voll) – Workflow korrigiert; Tag `v0.1.0` bleibt bestehen (Löschen nicht erlaubt)
- 2026-11-08 · Claude Code · Phasen 5–8 und Rest von 9 umgesetzt (Docker, Unraid, Desktop, Release, Kapseln, Builds, Abgleich, ZIP); T-0.6/0.7/0.10/1.13 erledigt; F-014 neu
- 2026-11-08 · Claude Code · F-004/F-005/F-010/F-013 entschieden (D-017–D-020); Phasen 0–4 und große Teile von 9 umgesetzt (Kern, CLI, Web, API, Git-HTTP, MCP); T-1.13 neu
- 2026-11-08 · Claude Code · T-0.5 gemergt und gepusht (erledigt)
- 2026-11-08 · Claude Code · T-0.5 atomares Schreiben + Pfadprüfung (internal/atomicfile, internal/safepath) → Review; F-013, T-0.13, T-1.12 neu
- 2026-11-08 · Claude Code · T-0.3 gemergt und gepusht (erledigt); Schema-Festlegungen von Dennis bestätigt
- 2026-11-08 · Claude Code · T-0.3 `repo.yaml`-Schema (docs/SCHEMA.md, internal/repometa) → Review
- 2026-11-08 · Claude Code · F-008 entschieden (7373, D-016); T-0.2 umgesetzt, gemergt und gepusht (erledigt); T-0.12 neu
- 2026-11-08 · Claude Code · T-0.1 gemergt (erledigt); T-0.11 privates GitHub-Repo angelegt und gepusht (erledigt)
- 2026-11-08 · Claude Code · F-002, F-003, F-012 entschieden (D-013–D-015); Git-Repo angelegt; T-0.1 umgesetzt → Review; T-0.11 neu
- 2026-11-08 · Claude · F-001 entschieden (privat, D-012); T-0.7 angepasst, T-0.10 neu
- 2026-11-08 · Claude · Konzept, UI-Entwurf, Projektführung angelegt (T-K.1–T-K.3)
