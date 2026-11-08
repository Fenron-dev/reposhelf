# Gitea/Forgejo – Übernahme-Kandidaten (T-0.6)

Gesichtet am 2026-11-08 im Gitea-Repository `go-gitea/gitea`, Stand
`8cb6fb2001ba538ecf3407a7d8848172d7ae5084` (Branch `main`, MIT). Alle Pfade
wurden über die GitHub-API geprüft (kein lokaler Klon, AGENTS.md
„Bauen und Ausführen“). Forgejo (GPL-3.0-or-later, D-015) hat die gleichen
Pfade; dort gilt dieselbe Bewertung.

**Ergebnis:** Für Phase 0–4 wurde **kein Code übernommen**. Die
RepoShelf-Anforderungen weichen an den entscheidenden Stellen ab (vor allem
die Force-Push-Sicherung und „YAML ist die Wahrheit“), und die Umsetzungen
sind klein genug, um sie ohne Gitea-Abhängigkeiten (ORM, Settings, Queues)
selbst zu schreiben. Die Tabelle hält fest, was angesehen wurde und warum.

| Thema | Pfad in Gitea | Bewertung | Umsetzung in RepoShelf |
|---|---|---|---|
| Pull-Mirror | `services/mirror/mirror_pull.go` | Gitea nutzt `git remote update --prune` mit `+refs/*:refs/*` – würde Geschichte löschen; für RepoShelf ungeeignet (Invariante 1). Nützlich: Fehlertexte, Umgang mit LFS. | `internal/gitcli/mirror.go` (Fetch ohne Zwang → Sicherung → gezielter Force-Fetch) |
| Mirror-API | `routers/api/v1/repo/mirror.go` | Nur Anstoßen eines Syncs; kein Mehrwert. | `POST /api/sync/…` |
| GitHub-Migration (Releases, Issues, Labels) | `services/migrations/github.go` | Hängt am Gitea-Downloader-Interface und Ratenbegrenzer; Feldzuordnung als Vorlage genutzt (Release-Assets über API-URL mit `Accept: application/octet-stream` für private Repos). | `internal/sources/providers.go` |
| Smart-HTTP | `routers/web/repo/githttp.go` | Eigene Implementierung des Protokolls mit Gitea-Rechten. RepoShelf nutzt stattdessen `git http-backend` per CGI (D-006, einfacher, voll kompatibel). | `internal/githttp` |
| Markdown | `modules/markup/markdown/markdown.go`, `modules/markup/sanitizer.go` | goldmark-Konfiguration (GFM, Fußnoten) übernommen als Idee; der Sanitizer (bluemonday) ist nicht nötig, weil RepoShelf rohes HTML in READMEs gar nicht ausgibt (goldmark ohne `WithUnsafe`) und eine strenge CSP setzt. | `internal/web/web.go` |
| Lizenz-Erkennung | `modules/repository/license.go`, `services/repository/license.go` | Gitea vergleicht mit dem vollständigen SPDX-Lizenztext (große Datenbasis). RepoShelf nimmt zuerst die API-Angabe und erkennt sonst die häufigsten Lizenzen an Kennsätzen. Bei Bedarf später ausbauen. | `internal/core/extras.go` (`detectLicense`) |
| Sprachstatistik | `modules/git/languagestats` | Für die Hauptsprache genügt die Host-API (`/languages`); eigene Auswertung erst, wenn Repos ohne API (beliebige Git-URL) eine Sprache brauchen. | – |
| MCP-Server | `gitea/gitea-mcp` (eigenes Repo, MIT) | Werkzeugzuschnitt verglichen; RepoShelf folgt Konzept 6.13 (Lesen/Organisieren/Bauen, kein Löschen). | `internal/mcp` |

Wird später doch Code übernommen: Kopfkommentar mit Repo, Pfad, Commit und
Lizenzhinweis in der Datei und Eintrag in `THIRD_PARTY.md` (AGENTS.md).
