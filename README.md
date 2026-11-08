# RepoShelf

Ein schlanker, selbst gehosteter Archiv- und Bereitstellungsort für
Git-Repositories und Entwicklungsprojekte – portabel auf dem Desktop oder als
Docker-Container (z. B. auf Unraid).

- Repos per Link spiegeln, einmalig oder dauerhaft aktuell halten
- Nichts geht verloren: Schutz vor Force-Push und verschwundenen Originalen
- Versionen, Releases und Builds mit Aufbewahrungsregeln und Papierkorb
- Ordnung über Schlagwörter, Sammlungen, Beziehungen und Backlinks
- Wichtige Projekte baubar halten (Abhängigkeiten werden mitgesichert)
- Builds auf eigenen Runnern (Unraid, Desktop, Workstation) oder GitHub Actions
- Für KI-Agenten nutzbar: als Git-Remote und über MCP

**Stand:** Kern, Kommandozeile, Weboberfläche, Git-HTTP, MCP, Aufbewahrung,
Docker und Desktop-Hülle sind umgesetzt. Details: `docs/STATUS.md`.
Bedienung und Installation: [`docs/BETRIEB.md`](docs/BETRIEB.md).

## Schnellstart

```bash
go build -o bin/reposhelf ./cmd/reposhelf
./bin/reposhelf init ~/RepoShelf-Daten
./bin/reposhelf add https://github.com/owner/name
./bin/reposhelf serve            # http://127.0.0.1:7373
```

## Bauen

Voraussetzung: Go (Version siehe `go.mod`) und `git`.

```bash
go build -o bin/reposhelf ./cmd/reposhelf
go test ./...
```

Desktop-App (Wails v2, nur in CI): `cd cmd/reposhelf-desktop && wails build`.
Docker-Image und Releases baut `.github/workflows/release.yml` bei einem `v*`-Tag.

## Dokumentation

| Datei | Inhalt |
|---|---|
| [`docs/BETRIEB.md`](docs/BETRIEB.md) | Installation, Bedienung, Agenten anbinden |
| [`docs/STATUS.md`](docs/STATUS.md) | Was offen, in Arbeit und erledigt ist |
| [`docs/KONZEPT.md`](docs/KONZEPT.md) | Funktionen, Architektur, Phasen |
| [`docs/ENTSCHEIDUNGEN.md`](docs/ENTSCHEIDUNGEN.md) | Grundsatzentscheidungen |
| [`AGENTS.md`](AGENTS.md) | Arbeitsregeln für Agenten und Menschen |
| [`design/`](design/README.md) | UI-Entwurf |

## Lizenz

RepoShelf steht unter der GNU General Public License, Version 3 oder neuer
(`GPL-3.0-or-later`). Volltext: [`LICENSE`](LICENSE).
