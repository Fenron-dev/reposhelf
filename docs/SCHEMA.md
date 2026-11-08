# RepoShelf – Schema von `repo.yaml`

> **Verbindlich** (T-0.3). Umgesetzt in `internal/repometa`. Bei Widerspruch
> gilt der Code; ein gefundener Widerspruch wird hier behoben.
> Grundlage: Konzept 5.2 (Entwurf), D-004 („YAML ist die Wahrheit“).

- **Ort:** `meta/<host>/<owner>/<name>/repo.yaml` im Datenordner.
- **Aktuelle Schema-Version:** `1`
- **Größe:** höchstens 1 MiB.

## Grundregeln

1. **Von Hand bearbeitbar.** Kommentare, Reihenfolge der Schlüssel und
   Schreibweise (z. B. `[a, b]`) bleiben beim Speichern erhalten. Nur
   geänderte Werte werden neu geschrieben.
2. **Unbekannte Schlüssel bleiben erhalten**, auf jeder Ebene, auch in
   Einträgen von `relations`. Beim Lesen gibt es dazu eine Warnung (Hilfe
   gegen Tippfehler), keinen Fehler. Damit kann eine ältere RepoShelf-Version
   eine Datei einer neueren lesen und speichern, ohne Daten zu verlieren.
3. **Alle bekannten Schlüssel werden geschrieben**, auch wenn sie `null`
   sind. So sieht man beim Bearbeiten, welche Einstellungen es gibt.
   Ausnahme: `relations[].source` fehlt, wenn leer.
4. **`null` heißt „erben“** bei Aufbewahrungswerten (global → Sammlung →
   Repo, Konzept 6.10).
5. **Keine Geheimnisse.** URLs mit Zugangsdaten werden abgelehnt.
6. **Schema-Nummer:** fehlt sie → Fehler. Ist sie höher als die unterstützte
   → Fehler „von neuerer Version geschrieben“ (die Bedeutung bekannter Felder
   könnte sich geändert haben). Ältere Nummern werden beim Lesen migriert.
7. **Alle Fehler auf einmal:** Die Prüfung meldet jedes ungültige Feld mit
   Pfad (z. B. `relations[1].target`), nicht nur das erste.
8. Die Datei wird **atomar** geschrieben (`internal/atomicfile`). Pfade
   entstehen nur über `internal/safepath`; die Prüfung von Segmenten hier ist
   dieselbe, damit jede gültige Datei auch gespeichert werden kann.

## Beispiel

```yaml
schema: 1
id: 01JC8QH3K7Z9X2M4N6P8R0T2V4
source:
  url: https://github.com/Fenron-dev/Fero
  host: github.com
  owner: Fenron-dev
  name: Fero
kind: own
fork_of: null
sync:
  mode: follow
  interval: 6h
  last_success: 2026-11-08T10:48:00Z
  last_check: 2026-11-08T10:48:00Z
  last_error: null
status_override: null
include:
  releases: true
  lfs: true
  submodules: true
  issues: true
  wiki: false
tags: [medien/beschaffung, rust, tauri]
collections: [fundus-oekosystem]
relations:
  - {type: delivers_to, target: github.com/Fenron-dev/Fundus2, state: confirmed}
  - {type: successor_of, target: github.com/Fenron-dev/MediaShelf, state: confirmed, source: manual}
retention:
  releases_keep: 5
  pinned: [v0.1.0]
  snapshots: {every: 30d, keep_for: 180d}
  force_push_backups: {keep_for: 180d}
buildable:
  enabled: true
  capsules_keep: 3
license:
  spdx: null
  checked: 2026-11-08
created: 2026-11-08T10:00:00Z
```

## Felder

| Schlüssel | Typ | Pflicht | Standard | Regeln |
|---|---|---|---|---|
| `schema` | Zahl | ja | – | `1` |
| `id` | ULID | ja | – | 26 Zeichen, Crockford-Base32, Großbuchstaben, erstes Zeichen ≤ `7`. Bleibt bei Umbenennung gleich. |
| `source.url` | URL | ja | – | Schema `https`, `http`, `ssh` oder `git`; mit Host. Keine Zugangsdaten; nur bei `ssh` ist ein Benutzername ohne Passwort erlaubt (`ssh://git@…`). Lokale Pfade/`file:` sind nicht erlaubt. |
| `source.host` | Host | ja | – | `hostname` oder `hostname:port` (Port 1–65535). Der Ordnername ist nur der Hostname (D-018); IPv6-Adressen werden nicht unterstützt. |
| `source.owner` | Segment(e) | ja | – | ein oder mehrere Segmente mit `/` (verschachtelte GitLab-Gruppen) |
| `source.name` | Segment | ja | – | siehe „Segment“ |
| `kind` | Auswahl | ja | – | `own` · `mirror` · `fork` |
| `fork_of` | Verweis | bei `fork` | `null` | nur bei `kind: fork` erlaubt und dort Pflicht; nicht auf sich selbst |
| `sync.mode` | Auswahl | ja | – | `once` · `follow` · `manual` |
| `sync.interval` | Dauer | bei `follow` | `null` | mindestens `5m` |
| `sync.last_success` | Zeitpunkt | nein | `null` | RFC 3339, UTC empfohlen |
| `sync.last_check` | Zeitpunkt | nein | `null` | RFC 3339 |
| `sync.last_error` | Text | nein | `null` | letzte Fehlermeldung |
| `status_override` | Auswahl | nein | `null` | vom Nutzer gesetzt, schlägt den erkannten Status: `ok` · `degraded` · `gone` · `archived` |
| `include.releases` | ja/nein | nein | `true` | |
| `include.lfs` | ja/nein | nein | `true` | |
| `include.submodules` | ja/nein | nein | `true` | |
| `include.issues` | ja/nein | nein | `true` bei `own`, sonst `false` | |
| `include.wiki` | ja/nein | nein | `false` | |
| `tags` | Liste | nein | `[]` | Hierarchie mit `/` (`ttrpg/tool`); jede Ebene ein Segment; kein `*` (für Filter reserviert); keine Doppelten |
| `collections` | Liste | nein | `[]` | Kennungen von Sammlungen; je ein Segment; keine Doppelten |
| `relations` | Liste | nein | `[]` | siehe „Beziehungen“ |
| `retention.releases_keep` | Zahl | nein | `null` (erben) | ≥ 0; `0` = nur angeheftete behalten |
| `retention.pinned` | Liste | nein | `[]` | Versionsnamen (Git-Tags), die immer bleiben; keine Doppelten |
| `retention.snapshots.every` | Dauer | nein | `null` (erben) | > 0 |
| `retention.snapshots.keep_for` | Dauer | nein | `null` (erben) | > 0 |
| `retention.force_push_backups.keep_for` | Dauer | nein | `null` (erben) | > 0 |
| `buildable.enabled` | ja/nein | nein | `false` | |
| `buildable.capsules_keep` | Zahl | nein | `null` (erben) | ≥ 0 |
| `license.spdx` | Text | nein | `null` | SPDX-Kennung; `null` = nicht erkannt → Warnung in der Oberfläche |
| `license.checked` | Datum | nein | `null` | `YYYY-MM-DD` |
| `created` | Zeitpunkt | ja | – | RFC 3339 |

Fehlende Abschnitte in einer handgeschriebenen Datei bekommen die Standardwerte.
Beim nächsten Speichern werden sie ergänzt.

### Beziehungen (`relations[]`)

Gerichtete Verbindung von **diesem** Repo zu einem anderen (Konzept 6.7). Die
Gegenseite (Backlink) wird nur abgeleitet, nie im anderen Repo gespeichert.

| Schlüssel | Pflicht | Regeln |
|---|---|---|
| `type` | ja | `snake_case` (`^[a-z][a-z0-9_]*$`). Bekannt: `plugin_of`, `depends_on`, `fork_of`, `successor_of`, `delivers_to`, `uses_code_from`, `related`. Andere sind erlaubt (erweiterbar). |
| `target` | ja | Verweis, nicht auf sich selbst |
| `state` | nein | `confirmed` (Standard) · `suggested` · `rejected`. Abgelehnte Vorschläge bleiben gespeichert, damit sie nicht erneut angeboten werden. |
| `source` | nein | wie die Beziehung gefunden wurde, z. B. `manual`, `readme`, `dependency`, `fork`, `wikilink` |

`type` + `target` zusammen sind eindeutig. Über dieses Paar werden Einträge
beim Speichern wiedererkannt, damit ihre unbekannten Schlüssel erhalten
bleiben, auch wenn die Liste umsortiert wird.

## Wertarten

- **Segment:** ein Datei- oder Ordnername, der auf macOS, Linux **und**
  Windows funktioniert (der Datenordner ist portabel):
  - nicht leer, nicht `.` oder `..`, höchstens 255 Bytes, gültiges UTF-8;
  - keine Steuerzeichen, kein `/` oder `\`, keines von `<>:"|?*`;
  - keine Leerzeichen am Anfang/Ende, kein Punkt am Ende;
  - kein Windows-Gerätename (`CON`, `PRN`, `AUX`, `NUL`, `COM0`–`COM9`,
    `LPT0`–`LPT9`, `CONIN$`, `CONOUT$`), auch nicht mit Endung (`nul.txt`)
    und in keiner Schreibweise;
  - beginnt nicht mit `.reposhelf-tmp-` (Zwischendateien beim Schreiben).

  Ein Host mit Port (`gitea.local:3000`) wird deshalb ohne Port zum Ordner
  (`gitea.local`, D-018).
- **Verweis:** die ULID eines Repos oder `host/owner/name` (mindestens drei
  Segmente; der Host darf einen Port haben).
- **Dauer:** ganze Zahlen mit Einheiten in fester Reihenfolge `d`, `h`, `m`,
  `s`, z. B. `30d`, `6h`, `1d12h`, `90m`. Keine Brüche, keine Wochen.
  Geschrieben wird die kanonische Form (`36h` → `1d12h`).
- **Datum:** `YYYY-MM-DD` (ohne Anführungszeichen).
- **Zeitpunkt:** RFC 3339, z. B. `2026-11-08T10:48:00Z`.

## Versionen und Migration

| Schema | Seit | Änderung |
|---|---|---|
| 1 | T-0.3 | erste Version |

Neue Schema-Version:

1. `CurrentSchema` in `internal/repometa/types.go` erhöhen.
2. Migrationsschritt in `internal/repometa/migrate.go` eintragen. Er arbeitet
   auf dem YAML-Baum, damit unbekannte Schlüssel und Kommentare erhalten
   bleiben.
3. Die Fixtures der Vorversion in `internal/repometa/testdata/` bleiben
   **unverändert** und müssen weiter gelesen werden (Test).
4. Diese Datei und die Tabelle oben ergänzen.
