# RepoShelf – Konzept

> **Status:** Entwurf 1.0 · Stand 2026-11-08
>
> Dieses Dokument beschreibt das **Soll**. Was tatsächlich gebaut ist, steht in
> `docs/STATUS.md`. Getroffene Entscheidungen mit Begründung stehen in
> `docs/ENTSCHEIDUNGEN.md`. Bei Widerspruch gilt:
> **Code › STATUS.md › ENTSCHEIDUNGEN.md › KONZEPT.md.**
> Wer einen Widerspruch findet, korrigiert das nachrangige Dokument oder trägt
> ihn als offene Frage in `docs/STATUS.md` ein – nie stillschweigend übergehen.
>
> UI-Entwurf: `design/` (Quelldateien) bzw. Online-Canvas, siehe `design/README.md`.

---

## Inhalt

1. Ziel und Abgrenzung
2. Leitsätze
3. Begriffe
4. Architektur
5. Datenablage
6. Funktionen
7. Übernahmen aus Gitea, Forgejo und eigenen Projekten
8. Phasenplan
9. Risiken und Fallstricke
10. Nicht-Ziele

---

## 1. Ziel und Abgrenzung

RepoShelf ist ein **schlanker, selbst gehosteter Archiv- und Bereitstellungsort
für Git-Repositories und Entwicklungsprojekte** – portabel auf dem Mac, als
Docker-Container auf Unraid oder auf einem beliebigen Rechner.

Es soll:

- Repos per Link übernehmen (GitHub zuerst, später GitLab, Codeberg/Gitea/Forgejo,
  beliebige Git-URLs und ZIP-Archive) und sie **dauerhaft sichern**, auch wenn das
  Original verschwindet.
- Pro Repo festlegen, ob es **einmalig** gesichert oder **aktuell gehalten** wird.
- Versionen, Releases und Build-Ergebnisse nach **Aufbewahrungsregeln** verwalten;
  Löschen geschieht nur über einen Papierkorb und nach Bestätigung beim Start.
- Projekte über **Schlagwörter, Sammlungen, Beziehungen und Backlinks**
  organisieren; Metadaten liegen lesbar als YAML neben den Daten.
- Wichtige Projekte **baubar halten**: Abhängigkeiten und Toolchain-Version
  werden mitgesichert.
- **Builds** auf Runnern ausführen (Unraid, Desktop, starker PC, extern GitHub
  Actions) und die Ergebnisse ins Archiv übernehmen.
- Von **KI-Agenten** (Claude Code, Codex, lokale LLMs) über **MCP** und als
  normales **Git-Remote** nutzbar sein.
- Zwischen Instanzen (Unraid ↔ Desktop) **auf Wunsch abgleichen**.

RepoShelf ist **kein GitHub-Nachbau**: keine Pull Requests, keine
Organisationen/Teams, keine Benutzerverwaltung für Fremde, kein Code-Review-
Workflow (siehe Kapitel 10).

---

## 2. Leitsätze

Diese Regeln gelten für jede Entscheidung im Projekt. Sie stammen zum Teil aus
Fero, Fundus2, OracleVault und MediaShelf.

1. **Git ist das Fundament.** Git speichert die gesamte Geschichte. RepoShelf
   verwaltet, organisiert und stellt bereit – es erfindet keine eigene
   Versionierung für Code.
2. **Archiv vor Aktualität.** Ein Abgleich darf nie Geschichte vernichten. Vor
   jedem Fetch wird der bisherige Stand gesichert (Force-Push-Schutz).
3. **Nichts verschwindet still.** Gelöscht wird nur über den Papierkorb, nach
   Bestätigung. RepoShelf löscht nichts, was es nicht selbst geschrieben hat.
4. **Nie stillschweigend einen Ort wählen.** Ist ein Datenordner oder Ziel nicht
   erreichbar, wird gemeldet – nicht auf einen anderen Ort ausgewichen. (Fero)
5. **YAML ist die Wahrheit, SQLite ist der Index.** Metadaten liegen lesbar und
   von Hand bearbeitbar als YAML. Die Datenbank ist jederzeit aus den Dateien neu
   aufbaubar.
6. **Ansicht ≠ Ablage.** Ordner, Sammlungen und Filter sind Sichten auf dieselben
   Repos. (Fundus2)
7. **Herkunft ist eine Eigenschaft, keine Ansicht.** Eigen / Spiegel / Fork /
   Upstream weg sind Werte einer Spalte – keine eigenen Bereiche. (Fundus2)
8. **Ein Programm, zwei Gesichter.** Server (Docker) und Desktop sind dieselbe
   Binärdatei; der Desktop legt nur ein natives Fenster darum.
9. **Agenten sind Erstnutzer.** Jede Funktion, die ein Mensch in der Oberfläche
   hat, soll – wo sinnvoll – auch über MCP oder API erreichbar sein. Zerstörende
   Aktionen bleiben dem Menschen vorbehalten.
10. **Zustandsdateien werden atomar geschrieben** (temp-Datei im selben Ordner,
    dann `rename`). (Fero `core/atomic.rs`)
11. **Leerzustände nennen ihre Ursache.** „Keine Treffer“ ohne Hinweis auf den
    aktiven Filter ist eine Sackgasse. (Fundus2)
12. **Deutsch in der Oberfläche, Englisch im Code.**

---

## 3. Begriffe

| Begriff | Bedeutung in RepoShelf |
|---|---|
| **Repo** | Ein verwaltetes Git-Repository mit seinen Metadaten. |
| **Art** (`kind`) | `own` (eigenes Projekt), `mirror` (Spiegel, schreibgeschützt), `fork` (beschreibbare Kopie eines Spiegels, mit Original verknüpft). |
| **Spiegel** | Vollständige Kopie aller Branches, Tags und Notes (`git clone --mirror`). Wird nie von Hand verändert. |
| **Fork** | Aus einem Spiegel abgeleitetes, beschreibbares Repo. Behält die Beziehung `fork_of`. Typischer Fall: ein Projekt ist verschwunden und wird weitergeführt. |
| **Branch** | Parallele Entwicklungslinie in einem Repo. Kommt mit dem Spiegel automatisch mit. |
| **Version** | Git-Tag (z. B. `v1.2.0`). In der UI „Version“, nie „Tag“, um Verwechslung zu vermeiden. |
| **Schlagwort** | Frei vergebenes Meta-Tag, hierarchisch möglich (`ttrpg/tool`). Nicht zu verwechseln mit Version. |
| **Sammlung** | Manuell zusammengestellte Gruppe von Repos. |
| **Smarte Sammlung** | Gespeicherter Filter (regelbasiert). |
| **Beziehung** | Gerichtete Verbindung zwischen zwei Repos/Notizen (z. B. `plugin_of`). |
| **Backlink** | Die Rückseite einer Beziehung, automatisch angezeigt. |
| **Release** | Version plus Beschreibung plus angehängte Dateien (Assets). |
| **Snapshot** | Zeitpunkt-Sicherung des Ref-Stands (z. B. monatlich), unabhängig von Releases. |
| **Force-Push-Sicherung** | Automatische Sicherung der Refs vor einem Abgleich, wenn sich Geschichte geändert hätte. |
| **Build-Kapsel** | Pro festgehaltener Version: gesicherte Abhängigkeiten + Toolchain-Version (+ optional Build-Ergebnis), damit offline gebaut werden kann. |
| **Instanz** | Eine laufende RepoShelf-Installation (Unraid, MacBook, Workstation). |
| **Runner** | Rolle einer Instanz (oder extern: GitHub Actions), die Builds ausführt. |
| **Aufbewahrungsregel** | Regel, was behalten wird (z. B. „neueste 5 Releases“). Ebenen: global → Sammlung → Repo. |
| **Anheften** | Markiert eine Version/Kapsel/Snapshot als „immer behalten“; Regeln greifen nicht. |

---

## 4. Architektur

### 4.1 Überblick

```
                ┌───────────────────────────────────────────────┐
                │                reposhelf (Go)                 │
                │                                               │
  Browser ─────▶│  web      Weboberfläche (HTML + htmx)         │
  Wails-Fenster │  api      REST/JSON für UI und Skripte        │
                │  githttp  Git Smart-HTTP (clone/fetch/push)   │◀──── git, Claude Code, Codex
                │  mcp      MCP-Server (stdio + Streamable HTTP)│◀──── Claude, Codex, LM Studio, ChatGPT
                │  ─────────────────────────────────────────    │
                │  core     Repos, Spiegel-Engine, Metadaten,   │
                │           Aufbewahrung, Papierkorb, Suche     │
                │  sources  GitHub, GitLab, Gitea/Forgejo, git  │────▶ github.com usw.
                │  capsule  Abhängigkeiten sichern              │
                │  runner   Builds in Containern / extern       │
                │  peer     Abgleich zwischen Instanzen         │◀───▶ andere Instanz
                │  store    YAML (Wahrheit) + SQLite (Index)    │
                └───────────────────────────────────────────────┘
```

### 4.2 Technik

| Bereich | Wahl | Begründung / Hinweis |
|---|---|---|
| Sprache | **Go** | Gitea (MIT) als direkte Code-Quelle; schnelle Kompilierung, lokal auf dem MacBook baubar; eine statische Binärdatei. Siehe D-001. |
| Git-Operationen | **System-`git` + `git-lfs`** über CLI | Robust, volle Funktion (Mirror, LFS, `http-backend`). Gitea macht es ebenso. `go-git` höchstens für reine Lesezugriffe. Siehe D-006. |
| Weboberfläche | Go-Templates + **htmx**, wenig eigenes JS, in die Binärdatei eingebettet (`embed`) | Kein Node-Build nötig. Muster aus Fero (Frontend in Binary gebacken) und VaultApp-Konzept. Siehe F-005. |
| Markdown | **goldmark** (GFM) | Wird von Gitea genutzt; Tabellen, Aufgabenlisten, Autolinks. Code-Hervorhebung serverseitig (z. B. chroma). |
| Datenbank | **SQLite** (rein-Go-Treiber, z. B. `modernc.org/sqlite`, ohne CGO) mit **FTS5** | Nur Index; aus YAML neu aufbaubar. Cross-Compile ohne CGO. Treiberwahl bei T-0.4 prüfen (FTS5-Unterstützung). |
| Desktop-Hülle | **Wails** | Go-Gegenstück zu Tauri. v2 ist stabil, v3 ist seit August 2026 Beta. Entscheidung in Phase 5 (F-004). Architektur darf nicht von Wails abhängen: Wails zeigt nur die Weboberfläche. |
| MCP | **Offizielles Go-SDK** `github.com/modelcontextprotocol/go-sdk` | Stabile v1-Linie (zuletzt v1.8.0). Version beim Start festpinnen. |
| Container | Ein Image mit `reposhelf`, `git`, `git-lfs`; Runner-Builds in **eigenen** Containern | Unraid-Template (XML) für Community Apps später. |
| Konfiguration | `reposhelf.yaml` im Datenordner, Umgebungsvariablen überschreiben | Docker-freundlich. |
| Geheimnisse | Desktop: Schlüsselbund des Betriebssystems; Docker: Umgebungsvariablen/Secret-Datei | **Nie** in YAML-Metadaten oder im Datenordner im Klartext. |

### 4.3 Betriebsarten

| Modus | Start | Oberfläche | Typischer Ort |
|---|---|---|---|
| `server` | `reposhelf serve` | Browser, Port (Standard offen: F-008) | Docker auf Unraid |
| `desktop` | App-Bundle / `.exe` | Wails-Fenster, gleicher Server intern auf `127.0.0.1` | MacBook, Windows-PC |
| `cli` | `reposhelf add <url>`, `reposhelf sync`, … | Terminal | überall, auch für Agenten |
| `mcp-stdio` | `reposhelf mcp` | – | lokal von Claude Desktop/Code, Codex, LM Studio gestartet |

### 4.4 Datenordner-Kette (aus Fero übernommen)

Reihenfolge (umgesetzt in `internal/datadir`, T-0.2):

1. Option `--data <ordner>` (Kommandozeile).
2. Umgebungsvariable `REPOSHELF_DATA`. Docker: Das Image setzt sie auf den
   festen Pfad `/data` (Volume).
3. Vom Nutzer **gewählter** Datenordner (Zeiger in der Datei
   `RepoShelf/datadir.txt` im Profilordner des Betriebssystems, z. B.
   `~/Library/Application Support/` auf macOS). Der Zeiger enthält einen
   absoluten Pfad.
4. `RepoShelf-Daten/` **neben der Programmdatei** (portabel, USB-Stick); bei
   einem macOS-App-Bundle neben dem `.app`, nicht darin. Symlinks auf die
   Programmdatei werden aufgelöst.

Nennt eine Stufe einen Ordner, der nicht erreichbar ist → **Meldung**, kein
Ausweichen auf die nächste Stufe. Nur Stufe 4 wird über ihr Vorhandensein
erkannt. Gibt es keine Stufe → Einrichtung. Nie stillschweigend nach
`~/.reposhelf` ausweichen.

`reposhelf config` zeigt den gefundenen Ordner, die Quelle und die wirksamen
Einstellungen.

---

## 5. Datenablage

### 5.1 Ordnerstruktur

```
RepoShelf-Daten/
├── reposhelf.yaml                 # Instanz-Konfiguration (ohne Geheimnisse)
├── repos/
│   └── github.com/
│       └── Fenron-dev/
│           └── Fero.git/          # Bare-Mirror (bzw. Bare-Repo bei own/fork)
├── meta/
│   └── github.com/
│       └── Fenron-dev/
│           └── Fero/
│               ├── repo.yaml      # Metadaten – die Wahrheit
│               ├── notes.md       # Notizen (Markdown, Wiki-Links)
│               ├── upstream.json  # Snapshot der Quell-Metadaten (Beschreibung, Topics, Lizenz …)
│               └── issues/        # nur wenn Issues aktiviert (eine Datei pro Issue)
├── releases/
│   └── github.com/Fenron-dev/Fero/v0.4.0/   # Release-Assets
├── capsules/
│   ├── blobs/sha256/…             # gemeinsamer, deduplizierter Abhängigkeits-Cache
│   └── github.com/Fenron-dev/Fero/v0.4.0.yaml  # Manifest der Kapsel
├── builds/                        # Build-Ergebnisse und Logs
├── collections/                   # Sammlungen und smarte Sammlungen (YAML)
├── notes/                         # freie Notizen, nicht an ein Repo gebunden
├── trash/                         # Papierkorb (mit Manifest: was, woher, wann, warum)
└── .reposhelf/
    ├── index.db                   # SQLite-Index (neu aufbaubar, nicht sichern nötig)
    ├── locks/
    └── logs/
```

Pfade werden aus `host/owner/name` gebildet. **Jedes Segment wird geprüft**
(kein `..`, keine leeren oder reservierten Namen, keine Steuerzeichen) – ein
Name aus dem Netz wird nie ungeprüft zum Pfad (Fero-Invariante).

### 5.2 `repo.yaml` – Schema (Entwurf)

```yaml
schema: 1
id: 01JC8Q…                    # ULID, stabil, auch nach Umbenennung
source:
  url: https://github.com/Fenron-dev/Fero
  host: github.com
  owner: Fenron-dev
  name: Fero
kind: own                      # own | mirror | fork
fork_of: null                  # bei kind: fork → ID oder host/owner/name
sync:
  mode: follow                 # once | follow | manual
  interval: 6h
  last_success: 2026-11-08T10:48:00Z
  last_check:   2026-11-08T10:48:00Z
  last_error:   null
status_override: null          # vom Nutzer gesetzt, schlägt Erkennung
include:
  releases: true
  lfs: true
  submodules: true
  issues: true                 # Standard: own → true, sonst false
  wiki: false
tags: [medien/beschaffung, rust, tauri]
collections: [fundus-oekosystem]
relations:
  - { type: delivers_to, target: github.com/Fenron-dev/Fundus2 }
  - { type: successor_of, target: github.com/Fenron-dev/MediaShelf, state: confirmed }
retention:
  releases_keep: 5
  pinned: [v0.1.0]
  snapshots: { every: 30d, keep_for: 180d }
  force_push_backups: { keep_for: 180d }
buildable:
  enabled: true
  capsules_keep: 3
license:
  spdx: null                   # automatisch erkannt; null = nicht erkannt → Warnung
  checked: 2026-11-08
created: 2026-11-08T10:00:00Z
```

- Unbekannte Felder werden **beim Schreiben erhalten** (Vorwärtskompatibilität,
  von Hand ergänzte Felder gehen nicht verloren).
- Jede Schema-Änderung bekommt eine Migration und einen Test, der eine Datei der
  Vorversion liest. (Fundus2)
- Das endgültige Schema wird in `docs/SCHEMA.md` festgeschrieben (T-0.3).

### 5.3 Index

SQLite enthält nur abgeleitete Daten: Repo-Liste, Schlagwörter, Beziehungen
(Edge-Tabelle), Volltextindex (README, Notizen, Beschreibungen), Build-Historie.
`reposhelf reindex` baut ihn komplett aus den Dateien neu auf.

**Achtung exFAT/USB:** SQLite im WAL-Modus ist auf exFAT/FAT unzuverlässig.
Auf solchen Dateisystemen `journal_mode=DELETE` verwenden (Erkennung beim Start).
Quelle: Fallstrick-Kapitel im VaultApp-Konzept (LW-Manager).

---

## 6. Funktionen

### 6.1 Repo hinzufügen

- Eingabe: Link. Erkennung von Host, Owner, Name; Abruf der Metadaten
  (Beschreibung, Topics, Sprache, Lizenz, Releases vorhanden, Wiki vorhanden).
- Auswahl: Art (Spiegel / Fork / Eigenes Projekt), Aktualisierung (einmalig /
  aktuell halten + Intervall), Mitsichern (Releases, LFS, Submodule, Issues, Wiki,
  Metadaten), Baubar halten, Schlagwörter (Vorschläge aus Topics), Sammlung.
- Ergebnis: `meta/…/repo.yaml` + `repos/….git` + Erstabgleich im Hintergrund.
- Auch per CLI (`reposhelf add <url> --tags …`) und MCP.
- Später: Massenimport (alle eigenen/gesternten GitHub-Repos), ZIP-Upload,
  beliebige Git-URL.

### 6.2 Spiegel und Abgleich

- Ablage als Bare-Mirror (`git clone --mirror`).
- **Vor jedem Fetch:** aktuellen Ref-Stand als
  `refs/reposhelf/backup/<zeitstempel>/…` sichern, **nur wenn** der Fetch
  Refs umschreiben oder löschen würde (Vorab-Vergleich per `git ls-remote`).
  Dadurch kostet der Normalfall nichts.
- Gelöschte Branches/Tags beim Original bleiben im Spiegel erhalten
  (kein `--prune` ohne Sicherung).
- **Zeitplan:** pro Repo Intervall; abgeschlossene/archivierte Repos nur noch
  alle 30 Tage prüfen (Fero `should_check`). Gleichmäßige Verteilung statt alle
  zur vollen Stunde.
- **Upstream-Zustand:** `erreichbar`, `vorübergehend gestört`, `Upstream weg`
  (404/gelöscht/privat geworden), `archiviert beim Original`.
- **Host-Gesundheit** (Fero `source_health`): Ein Host gilt erst als gestört, wenn
  **alle** aktiven Repos dieses Hosts seit mindestens 24 h scheitern – sonst
  wird ein GitHub-Ausfall als „Upstream weg“ bei 100 Repos fehlgedeutet.
- **Status-Rangfolge** (Fero): Nutzer-Override > erkannter Status > Altwerte.
- GitHub-API-Limits: ohne Token 60 Anfragen/Stunde, mit Token 5000/Stunde.
  Token wird dringend empfohlen; Abfragen werden gebündelt und gecacht.

### 6.3 Metadaten

- `upstream.json`: Snapshot von Beschreibung, Topics, Homepage, Lizenz (SPDX),
  Sprachen, Default-Branch, Archiv-Flag, Erstell-/Pushdatum. Bei jeder Änderung
  wird die alte Fassung in einer Historie behalten (wichtig, wenn das Original
  verschwindet).
- Lizenz-Erkennung: API-Angabe + Prüfung der `LICENSE`-Datei. Keine Lizenz →
  deutlicher Hinweis („alle Rechte vorbehalten“).

### 6.4 Releases und Assets

- Releases inkl. Beschreibung (Markdown) und Assets herunterladen.
- Aufbewahrung nach Regel (Standard: neueste 5) + angeheftete.
- Prüfsummen (SHA-256) speichern; Dubletten nur einmal ablegen.

### 6.5 Issues

- **Fremde Repos:** aus, pro Repo zuschaltbar.
- **Eigene Projekte:** an. Phase 9: GitHub-Issues spiegeln (lesend, mit
  Kommentaren, Labels). Lokale, eigene Issues direkt in RepoShelf sind eine
  offene Frage (F-006).
- Ablage als Markdown-Dateien mit YAML-Frontmatter (durchsuchbar, lesbar ohne App).

### 6.6 Schlagwörter und Sammlungen

- Schlagwörter hierarchisch mit `/` (`ttrpg/tool`, `ttrpg/plugin`); Filter
  `ttrpg/*` greift auf alle Unterebenen.
- Umbenennen und Zusammenführen von Schlagwörtern global (schreibt alle
  betroffenen `repo.yaml` atomar um).
- Optional Farbe/Icon pro Schlagwort und Sammlung (MediaShelf-Idee).
- **Smarte Sammlungen** als gespeicherter Filter `{logic, rules}` – Schema
  **kompatibel zu MediaShelf/OracleVault** (`SmartFilter`). Exaktes Schema vor
  der Umsetzung aus MediaShelf übernehmen (T-2.4).

### 6.7 Beziehungen und Backlinks

- Generisches Edge-Modell (OracleVault): `from`, `to`, `type`, `state`
  (`confirmed` | `suggested`), `source` (wie erkannt).
- Typen (erweiterbar): `plugin_of`, `depends_on`, `fork_of`, `successor_of`,
  `delivers_to`, `uses_code_from`, `related`.
- Jede Beziehung erscheint auf **beiden** Seiten; die Gegenseite heißt Backlink
  (z. B. „Plugins: …“ auf der Seite des Tools).
- **Automatische Vorschläge** aus:
  - Links in README/Notizen auf andere verwaltete Repos,
  - Abhängigkeitsdateien (`go.mod`, `Cargo.toml`, `pubspec.yaml`,
    `package.json`, `requirements.txt` – Git- und Pfad-Abhängigkeiten),
  - Fork-Herkunft (API),
  - Wiki-Links `[[Repo]]` in Notizen.
- Vorschläge bleiben `suggested`, bis der Nutzer bestätigt. Abgelehnte
  Vorschläge werden gemerkt und nicht erneut angeboten.
- Später: Graph-Ansicht „Beziehungen“.

### 6.8 Notizen

- Pro Repo `notes.md`, dazu freie Notizen unter `notes/`.
- Wiki-Links `[[Fero]]`, `[[Fero#Abschnitt]]` erzeugen Beziehungen (Backlinks).
- YAML-Frontmatter erlaubt (Schlagwörter, Datum).

### 6.9 Suche

- Volltext (SQLite FTS5) über Namen, Beschreibungen, README, Notizen, Issues.
- Filter: Art, Schlagwort, Sammlung, Status, Lizenz, Sprache, „baubar halten“.
- Befehlspalette `⌘K` (OracleVault-Idee).
- Code-Volltextsuche über alle Repos: später (F-007, z. B. zoekt).

### 6.10 Aufbewahrung und Papierkorb

- Regeln auf drei Ebenen: **global → Sammlung → Repo** (die spezifischere gewinnt).
- Was Regeln betreffen: Release-Assets, Snapshots, Force-Push-Sicherungen,
  Build-Kapseln, Build-Ergebnisse, ungenutzte Cache-Einträge, ganze Repos
  (nur manuell vorgemerkt).
- **Nie betroffen:** angeheftete Einträge, die Git-Geschichte selbst.
- **Beim Start:** Dialog „X Einträge sind zur Löschung vorgemerkt“ mit Grund,
  Größe, Auswahl; Aktionen „In Papierkorb“, „Anheften/Behalten“,
  „Beim nächsten Start erneut fragen“.
- **Papierkorb:** Einträge 30 Tage wiederherstellbar (konfigurierbar), danach
  endgültige Löschung – ebenfalls erst nach Bestätigung.
- Vorschau „Was würde die nächste Bereinigung entfernen?“ jederzeit abrufbar.

### 6.11 Baubar halten (Build-Kapseln)

Für als „baubar halten“ markierte Repos wird pro festgehaltener Version eine
Kapsel erstellt:

| Ökosystem | Erkennung | Sicherung |
|---|---|---|
| Go | `go.mod` | `go mod vendor` bzw. Modul-Cache |
| Rust | `Cargo.toml` | `cargo vendor` |
| Dart/Flutter | `pubspec.yaml` | Pub-Cache (`dart pub get` in isolierten Cache) |
| Node | `package.json` + Lockfile | Paket-Tarballs aus Lockfile |
| Python | `requirements*.txt` / `pyproject.toml` | `pip download` |
| Docker | `Dockerfile` | Basis-Images per `docker save` |

- Dazu: Toolchain-Version (z. B. `rust-toolchain.toml`, Go-Version, Flutter-Version),
  Build-Befehl, optional das fertige Build-Ergebnis.
- **Gemeinsamer, inhaltsadressierter Cache** (`capsules/blobs/sha256/…`): ein
  Paket, das zehn Projekte nutzen, liegt nur einmal auf der Platte.
- **Kapsel-Test:** periodischer Offline-Build aus der Kapsel auf einem Runner
  (ohne Netz), um zu beweisen, dass sie vollständig ist.
- Kapsel-Erstellung läuft in einem Container auf einem Runner, nicht auf dem
  MacBook (Speicher).

### 6.12 Builds und Runner

- Jede Instanz kann die **Runner-Rolle** einschalten und meldet ihre Fähigkeiten
  (`linux/amd64`, `android`, `macos/arm64`, `windows`, `docker`, `gpu`).
- Build-Definition: vorhandene `.github/workflows/*.yml` werden mit **`act`** in
  Docker ausgeführt; alternativ eine einfache `reposhelf-build.yaml`
  (Image, Befehle, Artefakte).
- **GitHub Actions als externer Runner:** Workflow per API anstoßen
  (`workflow_dispatch`), Ergebnis-Artefakte automatisch ins Archiv holen.
  Nötig für macOS/iOS-Builds, solange kein Mac-Runner verfügbar ist.
- **Einschränkung:** macOS- und iOS-Builds brauchen macOS mit Xcode; Unraid
  (Linux) kann sie nicht. Windows-Builds unter Linux nur eingeschränkt
  (Cross-Compile).
- Runner mit wenig Speicher (MacBook): Builds nur nach Bestätigung,
  Speicherprüfung vorab, Aufräumen danach.
- Agenten stoßen Builds per MCP an; Ergebnisse (Status, Log, Artefakt-Links)
  kommen über MCP zurück.
- Builds laufen in isolierten Containern mit begrenzten Ressourcen; Geheimnisse
  nur explizit pro Build freigegeben.

### 6.13 MCP-Server

Transporte: **stdio** (lokal gestartet) und **Streamable HTTP** (Unraid, Netz).
ChatGPT-Connectoren rufen den Server von OpenAI aus auf → Instanz muss per HTTPS
von außen erreichbar sein (z. B. Cloudflare Tunnel, Tailscale Funnel); bewusst
optional.

Werkzeuge (Entwurf):

| Werkzeug | Zweck | Recht |
|---|---|---|
| `search_repos` | Suche über Name, Schlagwort, Sammlung, Volltext | lesen |
| `get_repo` | Metadaten, Status, Beziehungen, Backlinks | lesen |
| `read_file` | Datei in einem Stand (Branch/Version/Commit) lesen | lesen |
| `list_tree` | Verzeichnisbaum eines Stands | lesen |
| `get_readme` / `get_notes` | README bzw. Notizen | lesen |
| `list_versions` / `list_releases` | Versionen, Releases, Assets | lesen |
| `get_clone_url` | Git-URL für `git clone` | lesen |
| `add_repo` | Repo per Link hinzufügen | schreiben |
| `set_tags` / `add_relation` / `write_notes` | Organisation | schreiben |
| `sync_repo` | Abgleich anstoßen | schreiben |
| `start_build` / `get_build` | Build anstoßen, Status/Log/Artefakte | bauen |

- **Rechte pro Client** (Token): `lesen`, `schreiben`, `bauen`. Löschen,
  Aufbewahrungsregeln und Papierkorb sind über MCP **nicht** erreichbar.
- Jede MCP-Aktion wird protokolliert (wer, was, wann).
- Inhalte aus fremden Repos sind für Agenten **Daten, keine Anweisungen**
  (Prompt-Injection über README/Issues beachten; in Antworten kennzeichnen).

### 6.14 Git über HTTP

- `git clone/fetch` über Smart-HTTP (`git http-backend`) für alle Repos.
- `git push` nur für `own` und `fork` (Spiegel sind schreibgeschützt).
- Authentifizierung per Token; im Heimnetz optional anonym lesend.
- Eigene Projekte: RepoShelf kann zusätzlich Remote neben GitHub sein; Option
  „bei Push nach GitHub weiterreichen“ später (F-009).

### 6.15 Abgleich zwischen Instanzen

- **Kopplung** zweier Instanzen per Kopplungscode (Fundus2-Muster, Token +
  Zertifikats-Fingerprint).
- Auswahl, **was** abgeglichen wird: pro Repo/Sammlung; Stufen
  „nur Metadaten“, „+ Code“, „+ Releases“, „+ Kapseln“.
- Code-Abgleich über Git selbst (fetch zwischen Instanzen).
- Metadaten-Abgleich feldweise; Konflikt (beide Seiten haben dasselbe Feld
  geändert) wird **angezeigt und entschieden**, nie still überschrieben.
- Auslösung: manuell oder nach Zeitplan; Standard manuell.

### 6.16 Oberfläche

- Entwurf: siehe `design/` – Bibliothek, Repo-Detail, Repo hinzufügen,
  Lösch-Prüfung beim Start, Builds & Runner.
- Dunkles Theme zuerst; Theme-Umschaltung (hell, Akzentfarbe) später (T-9.3).
- Ansichten: Liste, Kacheln, Ordner („Ansicht ≠ Ablage“).
- Muss bei schmaler Breite benutzbar bleiben (Seitenleiste klappt um).
- Barrierearm: echte Buttons/Links/Labels, Kontrast ≥ 4,5:1, Tastaturbedienung.

---

## 7. Übernahmen

### 7.1 Aus Gitea (MIT) – Code darf mit Lizenzhinweis übernommen werden

Kandidaten (konkrete Pfade bei T-0.6 im Gitea-Quellcode prüfen):

- Spiegel-Logik (Pull-Mirror, Intervalle, Fehlerbehandlung)
- Migrations-/Downloader-Code für GitHub (Releases, Issues, Labels, Wiki)
- Markdown-Darstellung (goldmark-Konfiguration, Sanitizer)
- Git Smart-HTTP-Bereitstellung
- Lizenz-Erkennung, Spracherkennung (Statistik)
- Gitea-MCP-Server (MIT) als Vorlage für Werkzeugzuschnitt

**Regel:** Übernommener Code bekommt einen Kopfkommentar mit Herkunft
(Repo, Pfad, Commit) und den MIT-Lizenzhinweis; Liste in `THIRD_PARTY.md`.

### 7.2 Aus Forgejo (GPL v3+ seit v9.0) – Code darf übernommen werden

RepoShelf steht selbst unter GPL-3.0-or-later (D-014), daher ist Forgejo-Code
übernehmbar (D-015). Gleiche Regel wie bei Gitea: Kopfkommentar mit Herkunft
(Repo, Pfad, Commit) und Lizenzhinweis; Liste in `THIRD_PARTY.md`.

### 7.3 Aus eigenen Projekten – Konzepte

| Quelle | Übernommenes Konzept |
|---|---|
| Fero | Atomares Schreiben; Host-Gesundheit; Abo-/Zeitplan-Modell mit 30-Tage-Ruhe; Status-Rangfolge (Nutzer schlägt Erkennung); Datenordner-Kette; Papierkorb-Ansicht; Frontend in Binary; „löscht nichts, was es nicht selbst geschrieben hat“; Pfad-Sanitizing; CI-Workflows für Format/Lockfile ohne lokale Toolchain |
| Fundus2 | „Ansicht ≠ Ablage“, „Herkunft ist Eigenschaft“; Desktop als Server im Heimnetz + Kopplung; relative Pfade; jede Schemaänderung mit Migration und Test; Leerzustände nennen Ursache |
| OracleVault | Vault als einfacher Ordner; Edge-Tabelle für Beziehungen; SmartFilter-Schema; Wiki-Links; Befehlspalette; LLM-Profile (OpenAI-kompatibel) |
| MediaShelf 2.0 | SQLite + YAML-Sidecars; Kategorien/Schlagwörter mit Farbe/Icon; Papierkorb + Rückgängig |
| LW-Manager (VaultApp-Konzept) | Fallstricke: SQLite/exFAT/WAL, macOS-Bundle-Pfade, Linux `noexec`; Go + htmx als Oberfläche |

---

## 8. Phasenplan

Jede Phase endet mit etwas Lauffähigem. Aufgaben und Stand: `docs/STATUS.md`.

| Phase | Ergebnis |
|---|---|
| **0 – Gerüst** | Go-Modul, Ordnerstruktur, Konfiguration, Datenordner-Kette, atomares Schreiben, `repo.yaml`-Schema + Validierung, SQLite-Index mit Neuaufbau, CI (vet, test, lint), Gitea-Quellen gesichtet |
| **1 – Spiegel-Kern** | Repo per Link/CLI hinzufügen, Mirror-Klon, Abgleich mit Zeitplan, Force-Push-Sicherung, Upstream-/Host-Status, Metadaten-Snapshot, Releases herunterladen, minimale Weboberfläche (Bibliothek, Detail, Hinzufügen) |
| **2 – Organisation** | Schlagwörter (hierarchisch), Sammlungen, smarte Sammlungen, Beziehungen + Backlinks + Vorschläge, Notizen mit Wiki-Links, Volltextsuche, Befehlspalette |
| **3 – Aufbewahrung** | Regeln auf drei Ebenen, Anheften, Lösch-Prüfung beim Start, Papierkorb mit Wiederherstellung |
| **4 – Bereitstellen für Agenten** | Git Smart-HTTP (clone/fetch, push für own/fork), MCP-Server (stdio + HTTP), Tokens mit Rechten, Protokoll |
| **5 – Verpackung** | Docker-Image + Unraid-Template, Desktop-App (Wails) für macOS/Windows, portabler Modus |
| **6 – Baubar halten** | Build-Kapseln je Ökosystem, gemeinsamer Cache, Kapsel-Manifest, Kapsel-Test |
| **7 – Builds & Runner** | Runner-Rolle, `act`-Ausführung, eigene Build-Definition, GitHub Actions als externer Runner, Artefakte ins Archiv, MCP-Werkzeuge für Builds |
| **8 – Abgleich** | Kopplung, selektiver Abgleich, Konfliktanzeige |
| **9 – Ausbau** | Issues spiegeln, weitere Quellen (GitLab, Codeberg, ZIP), Themes, Graph-Ansicht, Code-Suche |

---

## 9. Risiken und Fallstricke

| Risiko | Gegenmaßnahme |
|---|---|
| Force-Push/gelöschte Branches beim Original vernichten Archiv | Backup-Refs vor jedem verändernden Fetch; kein Prune ohne Sicherung |
| GitHub-Ausfall wird als „Upstream weg“ gedeutet | Host-Gesundheit (alle Repos eines Hosts) + Mindestdauer 24 h |
| API-Limits (60/h ohne Token) | Token, Bündelung, Caching, Abfragen verteilen |
| SQLite auf exFAT (USB) | WAL vermeiden, `journal_mode=DELETE` |
| Speicher wächst unkontrolliert (LFS, Assets, Kapseln) | Aufbewahrungsregeln, Größenanzeige, Dedup-Cache, Vorschau |
| Pfad-Injection über Repo-/Release-Namen | Strenges Segment-Sanitizing, nie Netzdaten direkt als Pfad |
| Prompt-Injection über fremde READMEs/Issues an Agenten | Inhalte als Daten kennzeichnen; MCP ohne Löschrechte |
| Fremder Code in Builds | Nur in Containern, ohne Geheimnisse, Ressourcenlimits |
| macOS-Bundle: portabler Datenordner „neben der App“ liegt im Bundle | Pfad relativ zum `.app`, nicht zur Binärdatei (VaultApp-Fallstrick) |
| Windows ohne `git` | Portables Git mitliefern oder Prüfung + Hinweis (F-010) |
| Lizenz: Gitea-Code ohne Hinweis übernommen | Herkunftskommentar + `THIRD_PARTY.md` Pflicht |
| Zwei Agenten bearbeiten gleichzeitig dieselbe Aufgabe | Status-Pflege in `docs/STATUS.md` vor Beginn (siehe AGENTS.md) |

---

## 10. Nicht-Ziele

- Pull Requests, Code-Review, Merge-Oberfläche (später evtl. „Agenten-Branch
  prüfen und übernehmen“ – eigene Entscheidung nötig)
- Organisationen, Teams, Fremdnutzer-Verwaltung
- Öffentliches Hosting für Dritte
- Eigene Versionsverwaltung neben Git
- Vollständige GitHub-API-Kompatibilität
