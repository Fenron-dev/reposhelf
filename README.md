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

**Stand:** Phase 0 – Grundgerüst. Siehe `docs/STATUS.md`.

## Bauen

Voraussetzung: Go (Version siehe `go.mod`) und `git`.

```bash
go build -o bin/reposhelf ./cmd/reposhelf
go test ./...
```

## Dokumentation

| Datei | Inhalt |
|---|---|
| [`docs/STATUS.md`](docs/STATUS.md) | Was offen, in Arbeit und erledigt ist |
| [`docs/KONZEPT.md`](docs/KONZEPT.md) | Funktionen, Architektur, Phasen |
| [`docs/ENTSCHEIDUNGEN.md`](docs/ENTSCHEIDUNGEN.md) | Grundsatzentscheidungen |
| [`AGENTS.md`](AGENTS.md) | Arbeitsregeln für Agenten und Menschen |
| [`design/`](design/README.md) | UI-Entwurf |

## Lizenz

RepoShelf steht unter der GNU General Public License, Version 3 oder neuer
(`GPL-3.0-or-later`). Volltext: [`LICENSE`](LICENSE).
