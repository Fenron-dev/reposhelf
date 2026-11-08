# RepoShelf – Betrieb und Bedienung

Kurzanleitung für Installation, erste Schritte und Anbindung von Agenten.
Was RepoShelf kann und warum, steht in `docs/KONZEPT.md`.

## 1. Starten

### Kommandozeile / portabel

```bash
reposhelf init                    # RepoShelf-Daten/ neben dem Programm anlegen (portabel, USB-Stick)
reposhelf init ~/RepoShelf-Daten  # oder einen eigenen Ordner wählen (wird als Zeiger gemerkt)
reposhelf serve                   # Weboberfläche: http://127.0.0.1:7373
```

RepoShelf sucht den Datenordner in dieser Reihenfolge: `--data`, Umgebungsvariable
`REPOSHELF_DATA`, der mit `init` gewählte Ordner, `RepoShelf-Daten/` neben dem
Programm. Ist ein genannter Ordner nicht erreichbar (z. B. USB-Stick nicht
eingesteckt), meldet RepoShelf das – es weicht nie auf einen anderen Ort aus.
`reposhelf config` zeigt, was gerade gilt; `reposhelf doctor` prüft alles.

Voraussetzung: **git** (ab 2.28). Unter Windows liegt ein portables Git im
Ordner `git/` neben dem Programm (D-020). Für große Dateien (LFS) zusätzlich
`git-lfs`.

### Docker / Unraid

```bash
docker run -d --name reposhelf -p 7373:7373 \
  -v /mnt/user/appdata/reposhelf:/data \
  -e REPOSHELF_ADMIN_TOKEN='langes-zufälliges-passwort' \
  -e REPOSHELF_GITHUB_TOKEN='ghp_…' \
  ghcr.io/fenron-dev/reposhelf:latest
```

Unraid: Vorlage `deploy/unraid/reposhelf.xml` nach
`/boot/config/plugins/dockerMan/templates-user/my-reposhelf.xml` kopieren, dann
unter „Docker → Add Container“ „RepoShelf“ wählen. Das Image ist privat
(GitHub Container Registry): auf Unraid einmal `docker login ghcr.io` mit einem
GitHub-Token (Recht `read:packages`).

Im Netz meldest du dich mit dem Admin-Token an. Weitere Tokens (für Git, Agenten)
legst du in der Oberfläche unter „KI-Zugriff“ an. Anonymes Lesen und Klonen im
Heimnetz kann in den Einstellungen erlaubt werden.

**Runner-Rolle im Container:** Docker-Socket einbinden
(`-v /var/run/docker.sock:/var/run/docker.sock`) und dem Container Zugriff geben
(`--group-add <GID von docker.sock>` oder als root laufen lassen). Dann in den
Einstellungen „Diese Instanz baut“ einschalten.

### Desktop (macOS, Windows)

`RepoShelf.app` (DMG) bzw. `RepoShelf.exe` (portables ZIP mit Git). Beim ersten
Start fragt die App nach dem Datenordner: portabel neben der App oder ein
eigener Ordner. Die App startet zusätzlich den Server auf `127.0.0.1:7373`, damit
Git und Agenten auf demselben Rechner zugreifen können.

## 2. Repos sichern

```bash
reposhelf add https://github.com/owner/name --tags rust,tool
reposhelf add https://codeberg.org/owner/name --mode once
reposhelf import                  # alle eigenen GitHub-Repos (braucht Token)
reposhelf import --starred        # alle mit Stern
reposhelf sync --all              # alles abgleichen
reposhelf list --tag ttrpg/*      # Schlagwort mit allen Unterebenen
```

In der Oberfläche: „+ Repo hinzufügen“, Link einfügen. Vor jedem Abgleich wird
geprüft, ob das Original Geschichte umgeschrieben hat; der alte Stand wird dann
unter `refs/reposhelf/backup/<Zeit>/` gesichert. Beim Original gelöschte
Branches bleiben erhalten.

**Tokens für Hosts** (nie im Datenordner gespeichert):
`REPOSHELF_GITHUB_TOKEN` (oder `GITHUB_TOKEN`), für andere Hosts
`REPOSHELF_TOKEN_<HOST>`, z. B. `REPOSHELF_TOKEN_CODEBERG_ORG`. Docker-Secrets:
gleicher Name mit `_FILE`. Auf dem Mac auch im Schlüsselbund:
`security add-generic-password -s RepoShelf -a github.com -w <token>`.

**GitHub-Actions-Artefakte:** Viele Projekte veröffentlichen Builds nur als
Actions-Artefakte (GitHub löscht sie nach einiger Zeit). Mit „GitHub-Actions-Artefakte“
beim Hinzufügen bzw. in den Repo-Einstellungen (CLI: `add --actions`) sichert RepoShelf
die Artefakte der neuesten Läufe – wie viele, stellst du global, je Sammlung oder je
Repo ein (`retention.actions_keep`, Standard 3). Ältere Läufe schlägt die Lösch-Prüfung
vor. Braucht ein GitHub-Token.

**Achtung Speicher:** „Releases mitsichern“ lädt die Dateien der neuesten
Releases (Standard: 5) – bei Projekten mit vielen Plattform-Builds können das
schnell Hunderte MB sein. Einstellbar global, je Sammlung und je Repo.

## 3. Ordnen

- **Schlagwörter** mit Hierarchie (`ttrpg/tool`); Filter `ttrpg/*`; global
  umbenennen/zusammenführen unter „Schlagwörter“ oder `reposhelf rename-tag`.
- **Sammlungen** (manuell) und **smarte Sammlungen** (gespeicherter Filter im
  Format `{logic, rules}` wie MediaShelf/OracleVault).
- **Beziehungen** (`plugin_of`, `depends_on`, …) mit Rückverweisen. Vorschläge
  entstehen aus README-Links, Abhängigkeitsdateien, Fork-Herkunft und Wiki-Links
  `[[Name]]` in Notizen; abgelehnte Vorschläge kommen nicht wieder.
- **Suche** über Name, Beschreibung, README und Notizen; ⌘K öffnet die
  Befehlspalette. **Code-Suche** über alle Repos: „Code-Suche“ in der
  Seitenleiste oder `reposhelf grep <text>` (mit `git grep`).

## 4. Aufbewahrung und Papierkorb

Regeln gelten global → Sammlung → Repo (das Spezifischere gewinnt). Was eine
Regel entfernen würde, zeigt „Lösch-Prüfung“ (`/cleanup`, `reposhelf cleanup`) –
beim Start fragt RepoShelf nach. Nichts wird ohne Bestätigung entfernt:
Einträge wandern in den **Papierkorb** und bleiben dort 30 Tage
wiederherstellbar; endgültig gelöscht wird nur nach erneuter Bestätigung.
Angeheftete Versionen und die Git-Geschichte selbst sind nie betroffen.

## 5. Agenten und Git

- **Git:** `git clone http://<server>:7373/git/<host>/<owner>/<name>.git`
  Benutzername beliebig, Passwort = Token. Pushen nur in eigene Projekte und
  Forks; Spiegel sind schreibgeschützt.
- **MCP lokal (stdio)** – Claude Code:
  `claude mcp add reposhelf -- /pfad/zu/reposhelf mcp --data /pfad/zu/RepoShelf-Daten`
  Claude Desktop / LM Studio / Codex: Befehl `reposhelf`, Argumente
  `["mcp", "--data", "…"]`. `--read-only` beschränkt auf Lesen.
- **MCP im Netz:** `http://<server>:7373/mcp` mit
  `Authorization: Bearer <Token>`. Die Rechte des Tokens bestimmen die
  Werkzeuge (lesen/schreiben/bauen). Löschen gibt es über MCP nicht.
- **REST:** `GET /api/repos`, `GET /api/repos/<id>`, `POST /api/repos`
  (`{"url": "…"}`), `POST /api/sync/<id>` – ebenfalls mit Bearer-Token.

## 6. Builds und Build-Kapseln

Runner-Rolle in den Einstellungen einschalten (braucht Docker). Ziele kommen aus

- `reposhelf-build.yaml` im Repo:

  ```yaml
  targets:
    - name: linux
      image: golang:1.24
      commands: ["go build -o dist/app ./cmd/app"]
      artifacts: ["dist/*"]
      network: false        # ohne Netz bauen
      secrets: [NPM_TOKEN]  # nur genannte Geheimnisse, aus REPOSHELF_SECRET_NPM_TOKEN
  ```
- Linux-Jobs aus `.github/workflows` (mit `act`, im Docker-Image enthalten),
- GitHub Actions als externer Runner (Workflows mit `workflow_dispatch`, Artefakte
  werden ins Archiv geholt; braucht `REPOSHELF_GITHUB_TOKEN`).

**Build-Kapseln** („Baubar halten“) sichern pro Version alle Abhängigkeiten
(Go, Rust, Dart/Flutter, Node mit `package-lock.json`, Python, Docker-Basis-Images)
in einem gemeinsamen, deduplizierten Speicher. Der **Kapsel-Test** baut ohne Netz
aus der Kapsel und beweist so, dass sie vollständig ist. Für die neueste Version
baubarer Repos wird automatisch eine Kapsel angelegt (mit Bestätigung, falls
eingestellt). Builds von Agenten (MCP) warten immer auf deine Bestätigung, wenn
„Jeden Build bestätigen“ an ist.

## 7. Abgleich zwischen Instanzen

„Abgleich“ in der Seitenleiste. Auf Instanz A „Kopplungscode anzeigen“, auf
Instanz B Adresse von A und Code eingeben – beide zeigen danach den Fingerprint
der anderen Seite; vergleichen. Pro gekoppelter Instanz wählst du, was
übernommen wird (alle Repos, eine Sammlung, einzelne Repos) und in welcher Stufe
(nur Metadaten, + Code, + Releases, + Kapseln). Abgeglichen wird manuell oder in
einem Intervall. Hat sich ein Feld auf beiden Seiten geändert, erscheint ein
Konflikt zum Entscheiden. Gekoppelte Instanzen mit Runner-Rolle können – wenn
freigegeben – Builds übernehmen.

Jede Instanz hat einen eigenen Schlüssel in `.reposhelf/instance.key` (nur für
den Besitzer lesbar). Gespeichert wird von der Gegenseite nur ihr öffentlicher
Schlüssel; Anfragen sind signiert.

## 8. Datenablage

```
RepoShelf-Daten/
├── reposhelf.yaml        Einstellungen (ohne Geheimnisse)
├── repos/<host>/<owner>/<name>.git    Bare-Spiegel
├── meta/<host>/<owner>/<name>/repo.yaml  Metadaten – die Wahrheit (von Hand bearbeitbar)
│                         upstream.json, notes.md, issues/
├── releases/…            Release-Dateien mit release.json (SHA-256)
├── capsules/             Build-Kapseln (gemeinsamer Cache unter blobs/)
├── builds/               Build-Ergebnisse und Logs
├── collections/          Sammlungen (YAML)
├── trash/                Papierkorb mit Manifest
└── .reposhelf/           Index (neu aufbaubar: reposhelf reindex), Logs, Tokens (nur Hashes)
```

Sichern genügt: alles außer `.reposhelf/index.db`.
