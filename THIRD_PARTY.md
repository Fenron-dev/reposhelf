# Fremdcode und Abhängigkeiten

RepoShelf steht unter **GPL-3.0-or-later** (D-014). Alle Abhängigkeiten sind
damit verträglich. Übernommener Quellcode aus Gitea/Forgejo (D-015) wird hier
mit Repo, Pfad, Commit und Lizenz eingetragen; bisher wurde **kein** Code
übernommen – Spiegel-Logik, Smart-HTTP und Markdown-Darstellung sind eigene
Umsetzungen (siehe `docs/GITEA-UEBERNAHMEN.md`).

## Eingebettete Dateien

| Datei | Herkunft | Version | Lizenz |
|---|---|---|---|
| `internal/web/static/htmx.min.js` | htmx (npm `htmx.org`), Integrität sha512 aus der npm-Registry geprüft | 2.0.11 | 0BSD |

## Mitgelieferte Programme

| Programm | Wo | Version | Lizenz |
|---|---|---|---|
| Git for Windows „MinGit“ | Windows-Pakete, Ordner `git/` (D-020), als eigenständiges Programm aufgerufen, nicht gelinkt | 2.56.0.windows.4, SHA-256 `e3bf7835…a877` | GPL-2.0 (Quellcode: github.com/git-for-windows/git) |
| nektos/act | Docker-Image, `/usr/local/bin/act` (Runner-Rolle) | 0.2.97, SHA-256 geprüft | MIT |
| git, git-lfs, docker-cli | Docker-Image (Alpine-Pakete) | Alpine 3.22 | GPL-2.0 / MIT / Apache-2.0 |

## Go-Module

| Modul | Lizenz |
|---|---|
| go.yaml.in/yaml/v3 | MIT und Apache-2.0 |
| modernc.org/sqlite, modernc.org/libc, modernc.org/mathutil, modernc.org/memory | BSD-3-Clause (SQLite selbst: Public Domain) |
| github.com/yuin/goldmark | MIT |
| github.com/modelcontextprotocol/go-sdk | MIT, Übergang zu Apache-2.0 |
| github.com/google/jsonschema-go | MIT |
| github.com/yosida95/uritemplate/v3 | BSD-3-Clause |
| github.com/segmentio/asm, github.com/segmentio/encoding | MIT |
| github.com/golang-jwt/jwt/v5 | MIT |
| github.com/google/uuid | BSD-3-Clause |
| github.com/dustin/go-humanize | MIT |
| github.com/mattn/go-isatty | MIT |
| github.com/ncruces/go-strftime | MIT |
| github.com/remyoudompheng/bigfft | BSD-3-Clause |
| golang.org/x/sys, x/text, x/net, x/sync, x/time, x/oauth2 | BSD-3-Clause |
| github.com/wailsapp/wails/v2 und Abhängigkeiten (nur Desktop-App) | MIT |

Prüfen: `go list -deps -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/... | sort -u`
