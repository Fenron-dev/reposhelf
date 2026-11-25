# RepoShelf – Status

> **Das ist die eine Stelle, an der steht, was offen, in Arbeit und erledigt ist.**
> Regeln zur Pflege: `AGENTS.md`, Abschnitt „Arbeitsablauf“.
> IDs werden **nie** neu vergeben oder umnummeriert; Zeilen werden **nie**
> gelöscht, nur im Status geändert.

**Statuswerte:** `offen` · `in Arbeit` · `Review` · `erledigt` · `blockiert` · `zurückgestellt`
**Wer:** `Dennis` · `Claude Code` · `Codex` · `Claude` (Chat)

---

## Aktueller Stand

- **Phase:** 0–9 umgesetzt; `v0.1.10` abgenommen, `v0.8.0` (alles seit v0.2.0, dazu T-9.21–T-9.32, R-028–R-032) zum Test bei Dennis
- **Letzte Änderung:** 2026-11-25 · Release `v0.8.0`: Speicherplanung, „Was ist neu“, Commits/Vergleich, Selbst-Aktualisierung (D-036, T-9.29–T-9.32)
- **Nächster Schritt:** Dennis testet `v0.8.0`; keine offenen Aufgaben (übrige Vorschläge V-7 bis V-18 nur auf Wunsch)

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

**Geprüft (Prüflauf `smoke.yml`, GitHub-Runner):** Docker-Image (Container, API, Abgleich,
Klonen, Anmeldeschutz), Windows-CLI mit MinGit, Windows-Desktop-App, echte Builds mit
Docker (Ziel aus `reposhelf-build.yaml` mit Artefakten, Go-Kapsel + Offline-Test, `act`,
GitHub Actions per workflow_dispatch). macOS-Desktop-App lokal gestartet und geprüft;
Einrichtungsdialog per Test (`internal/desktop`).
**Grenzen:** macOS-App nur ad-hoc signiert (Notarisierung braucht ein
Apple-Entwicklerkonto). Artefakt-Download von GitHub Actions gegen das echte GitHub geprüft (SoloSidian:
die zwei neuesten Läufe, ZIPs gültig, Prüfsummen stimmen) und per Test gegen eine
nachgebildete API (`internal/runner/github_test.go`).

**Offene Fragen:** keine (F-019 → D-034).

**2026-11-10 · Claude Code (T-9.8/T-9.9, Rückmeldung zu v0.1.7 unter Windows):**
Jeder Git-/Docker-/act-Aufruf startet ohne Konsolenfenster (`gitcli.HideWindow`).
Seiten starten im Normalfall keinen Git-Prozess mehr: Lösch-Prüfung im Hintergrund
(`internal/web/cache.go`, nach jeder Änderung neu), README/Commits je Abgleichstand
zwischengespeichert, Diagnose und Build-Kapsel-Panel werden nachgeladen.
`TestPageGitCost` wacht darüber. Abgleiche melden Phase und Git-Prozent
(Seitenleiste + Repo-Seite, die sich danach selbst neu lädt); oben ein Ladebalken
bei Seitenwechseln. Unter Windows noch nicht von Dennis bestätigt.

**2026-11-10 · Claude Code (T-9.10):** Tokens für Git-Hosts: Einstellungen →
„Zugänge zu Git-Hosts“ bzw. `reposhelf host-token`. Reihenfolge: Umgebungsvariable,
dann Schlüsselbund (`internal/sources/keychain_*.go`; Windows: Anmeldeinformations-
verwaltung, Eintrag `RepoShelf:<host>`). Neuer Token wird vorher beim Host geprüft;
abgelehnte werden nicht gespeichert. macOS-Schlüsselbund lokal geprüft
(`REPOSHELF_KEYCHAIN_TEST=1 go test ./internal/sources`), Windows im Prüflauf
(`smoke.yml`, Schritt „Host token“). Offen aus D-024: „Mit GitHub anmelden“ braucht
eine OAuth-App von Dennis.

**2026-11-10 · Claude Code (T-9.11–T-9.14):** Lokale Issues liegen unter
`meta/…/issues/local/<n>.md` (Kennung L<n>), gespiegelte bleiben schreibgeschützt.
Push-Weiterreichen: `sync.push_upstream`, nach Push über Git-HTTP (`RefreshAfterPush` →
`forwardPush`), Ergebnis in `upstream.json` (`forwarded`, `forward_error`). Kapseln:
Toolchain-Image als Blob `.reposhelf-capsule/toolchain/<id>.tar`, `image_id` im Manifest;
im echten Docker erst im Prüflauf `builds` belegt. Modelle: Provider `huggingface`
(`internal/sources/huggingface.go`), Erkennung `internal/core/models.go`. Lokal ist kein
git-lfs installiert: `TestLargeModelNeedsConfirmation` läuft nur in CI ganz durch.

**2026-11-11 · Claude Code (T-9.16, T-9.17):** Issue-Änderungen für das Original
stehen in `meta/…/issues/outbox.yaml` (`internal/core/issues.go`, `SendIssues`); ein
gesendetes lokales Issue bekommt `remote: <n>` und wird dann als gespiegeltes angezeigt.
Ordner-Übernahme in `internal/core/importdir.go` (Git ohne Arbeitskopie:
`internal/gitcli/commitfiles.go`), Papierkorb `internal/systrash` (Tests ersetzen ihn über
`trashMove`). Lokal kein git-lfs: `TestHFCacheFolder` läuft nur in CI ganz. Windows-
Papierkorb im Prüflauf belegt (`smoke.yml`, Schritt „Take over a folder …“).

**2026-11-11 · Claude Code (T-9.18, T-9.11 Rest):** LFS über Git-HTTP in
`internal/githttp/lfs.go` (Objekte in `lfs/objects` des Spiegels). Modell-Ordner für
Programme: `internal/core/hfhome.go`, nach jedem Abgleich; LFS-Objekte werden dabei
schreibgeschützt (0444), Papierkorb-Leeren nutzt deshalb `removeTree`. Python-Plattformen:
`pythonPlatforms` in `internal/runner/capsule.go` – nur per Unit-Test (Befehle) geprüft,
ein echter `pip download` für fremde Plattformen läuft erst beim nächsten Kapselbau.

**2026-11-11 · Claude Code (T-9.19):** Laufzeit-Image in `internal/runner/image.go`
(Ziel `image`, nur wenn ein `Dockerfile` im Projekt liegt; Artefakt
`<owner>-<name>-<version>.tar.gz`; `LoadImage` für den Knopf „In Docker laden“). Der Bau
läuft ohne Speichergrenze (docker build kennt `--memory` mit BuildKit nicht).

**2026-11-11 · Claude Code (Review, T-9.20):** Eigene Prüfung der Arbeiten T-9.16–T-9.18
ergab R-001–R-008 (alle behoben, `9364dc0`). Wichtig: Ordner-Übernahme läuft jetzt im
Abgleich-Job (`seedImport` am Anfang von `Sync`), nicht mehr im Web-Request; der
Datenordner selbst ist als Quelle gesperrt. macOS-Papierkorb über `/usr/bin/trash`
(den Ordner `~/.Trash` darf ein Programm ohne Sonderrecht nicht lesen). Ein kleiner
Prüf-Ordner `reposhelf-trash-probe` liegt seit dem Test in Dennis' Papierkorb.
Prüflauf `builds` baut jetzt auch eine Python-Kapsel mit Windows-Paketen (seit v0.5.2 grün).

**2026-11-11 · Claude Code (Prüfung):** Sicherheits- und Qualitätsprüfung auf Dennis'
Wunsch, ohne Codeänderung. Bericht: `docs/PRUEFBERICHT-2026-11.md`; Befunde R-011–R-027
(drei „hoch“: lokales Vertrauen/DNS-Rebinding, Geheimnisse im Build, `act`), Vorschläge
V-1–V-18 als F-019. `govulncheck` lief lokal (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`):
nur die Go-Version selbst betroffen. Befunde sind `offen` – Beheben erst nach Auftrag.

**2026-11-21 · Claude Code (R-011–R-014):** Lokales Vertrauen braucht jetzt einen lokalen
`Host`-Kopf (`localhost`, Loopback-IP, `server.public_url`); das Desktop-Fenster markiert
seine Anfragen mit `server.MarkLocal`. Wer RepoShelf über einen anderen Namen am selben
Rechner aufruft (z. B. `meinpc.local:7373`), muss sich jetzt anmelden oder diesen Namen als
`public_url` eintragen. API: POSTs mit fremder Herkunft oder ohne JSON → 403. Geheimnisse
nur mit `buildable.secrets` (Repo-Einstellungen, nur Verwalter). `act` ohne Docker-Socket
(`--container-daemon-socket -`), unsichere Workflows abgelehnt, Spiegel-Workflows warten
auf Bestätigung. Die Platte war zwischendurch voll; `go clean -cache` gab 5 GB frei.

**2026-11-21 · Claude Code (R-015–R-027):** Kopplung von Instanzen braucht jetzt auf beiden
Seiten v0.5.4 (Nonce gegen Wiederholung; ältere Gegenstellen bekommen eine klare Meldung).
Submodule werden nur noch vom selben Host und eine Ebene tief übernommen. Nach zehn
falschen Tokens von einer Adresse ruht die Prüfung fünf Minuten. Git läuft mit
`GIT_ALLOW_PROTOCOL` und `safe.directory=*`. Offen: R-023 (README-Bilder, hängt an F-019/V-4).

**2026-11-21 · Claude Code (T-9.21–T-9.24, D-034):** Integritätsprüfung
`internal/core/integrity.go` (läuft über den Zeitplan in `enqueueDue`, Bericht
`.reposhelf/integrity.json`); Benachrichtigungen `internal/core/notify.go`
(`.reposhelf/notifications.jsonl`, Auslöser in `Sync` über `notifySync`); README-Bilder
`internal/core/readmeassets.go` (Abruf nur an öffentliche Adressen, Prüfung beim Verbinden);
Wiederbeleben `internal/core/revive.go` + `internal/sources/publish.go`. Der
macOS-Schlüsselbund speichert jetzt beliebige Werte (Übergabe als Hex). Noch offen bei
V-2: Meldungen des Betriebssystems in der Desktop-App (Wails v2 hat dafür keine API).

**2026-11-22 · Claude Code (R-028, R-029):** README-Suche in `gitcli.Readme` folgt GitHub
(`.github/`, Hauptordner, `docs/`); der Name ist jetzt ein Pfad (`docs/README.md`), relative
Links/Bilder gelten ab dessen Ordner. Markdown wird mit HTML gerendert und danach als Ganzes
von `internal/htmlsafe` gefiltert (Positivliste, ids mit `user-content-`, Links/Bilder über
Hooks in `web.linkRewriter`). Wer Elemente oder Attribute freigibt: nur in `htmlsafe.allowed`,
mit Test in `htmlsafe_test.go`. Neu offen: T-9.25 (Hinweisblöcke). Release `v0.6.1`: Prüflauf grün,
CI rot wegen GO-2026-6860 in `x/net` (R-030) – behoben in `v0.6.2` (Prüflauf und CI grün).

**2026-11-22 · Claude Code (T-9.26–T-9.28, D-035):** Dateinamen-Erkennung in
`internal/assetclass` (keine Abhängigkeiten; Tests mit echten Namen von PhotoCraft,
Electron, Obsidian, Ollama, Zed, ripgrep). Wirksame Auswahl in `core.EffectiveRetention`
(`Files`, `Profile`; Reihenfolge Zahl: global → Sammlung → Profil → Repo). Abgleich filtert in
`syncReleases`; nicht mehr passende Dateien sind Aufräum-Vorschläge `release_files`
(`retention.go`). Oberfläche: ein Block `release-choice` (`web/releasechoice.go`,
`templates/releases.html`) für Hinzufügen, Repo-Einstellungen und Profil-Editor; ob ein Repo
dem Profil folgt oder eine eigene Auswahl hat, entscheidet, was die Häkchen auswählen. Die
Release-Liste des Hinzufügen-Dialogs wird 10 Minuten zwischengespeichert (API-Grenze).
Profilnamen sind nach dem Anlegen fest (Repos verweisen darauf). Im Browser mit PhotoCraft
geprüft (Profil „Desktop“: 33 statt 156 Dateien, 1,8 statt 8,7 GB). Release `v0.7.0`:
Release-Lauf, Prüflauf (docker, builds, windows) und CI grün.

**2026-11-24 · Claude Code (T-9.25):** Hinweisblöcke (`> [!NOTE]` usw.) erkennt ein
goldmark-Schritt in `internal/web/alert.go` (nur Zitate auf oberster Ebene, Markierung allein in
der ersten Zeile, danach Inhalt – wie GitHub); Titel deutsch mit Symbol. `htmlsafe` lässt
`class` nur mit genau diesen Hinweis-Klassen durch (`classRe`), sonst könnten READMEs das
Aussehen von RepoShelf-Bedienelementen übernehmen. Neu offen: R-031 (CSS-Zwischenspeicher).

**2026-11-25 · Claude Code (R-031):** `internal/web/static.go`: Hash über alle eingebetteten
Dateien, Vorlagen verlinken per `{{static "app.css"}}` → `/static/app.css?v=<hash>`
(`immutable`). Neue Dateien in `static/` immer über `static` verlinken, nie mit fester
Adresse – sonst gelten sie als unversioniert (`no-cache`, 304 per ETag). Release `v0.7.1`: Release-Lauf, Prüflauf (docker, builds,
windows) und CI grün.

**2026-11-25 · Claude Code (T-9.29, D-036):** Speicherplanung in `internal/core/storage.go`:
ein Messwert pro Tag (Bibliothek, jede Sammlung, freier Platz) in `.reposhelf/usage.jsonl`,
stündlich aktualisiert aus `enqueueDue` (auch bei pausiertem Abgleich). Zunahme per
Ausgleichsgerade über 90 Tage, Vorhersage erst ab 7 Tagen Verlauf. Obergrenze
`max_size_mb` in `collections/<id>.yaml` (Formular in GB); Warnung einmal beim
Überschreiten (Meldungsart `storage`), gemerkt in `Over`/`Short` des Messwerts. Seite
`/storage` (`web/storage.go`, `templates/storage.html`) mit SVG-Diagramm ohne Skript.

**2026-11-25 · Claude Code (T-9.30):** Jeder Abgleich schreibt, was ankam, als
`changes` in die Verlaufszeile (`SyncChanges`: Tags, Commits auf dem Hauptbranch per
`git rev-list --count`, Release-Dateien, umgeschriebene Refs); ältere Zeilen ohne `changes`
zählen nicht. `core.News` liest höchstens 5000 Verlaufszeilen. Der Block steht nur auf der
ungefilterten Bibliothek (`templates/news.html`); „letzter Besuch“ über zwei Cookies
(`web/news.go`, Pause 1 Stunde, erster Besuch = letzte Woche).

**2026-11-25 · Claude Code (T-9.31):** `internal/gitcli/history.go`: `LogPage`, `LogRange`,
`CommitDetail`, `Diff` (Dateiliste aus `--numstat -z`, Patch gestreamt bis 2 MB, Parser für
Umbenennungen, Binärdateien, Namen mit Leerzeichen). Merge-Commits zeigen die Änderung zum
ersten Elternteil. Seiten in `web/history.go`, `templates/history.html`. Nebenbei behoben
(`689b26c`, T-9.30): Besuchszeit in Millisekunden, sonst blieb nach „Alles gesehen“ die
laufende Sekunde „neu“.

**2026-11-25 · Claude Code (T-9.32):** `internal/update` (nur atomicfile/systrash):
`Check` (GitHub `releases/latest`, einmal am Tag über `Run`, Zustand in
`.reposhelf/update.json`), `Install` je `Flavor` (cli, windows-app: Archiv laden, gegen
`SHA256SUMS`/`<datei>.sha256` prüfen, nur den einen Eintrag entpacken, Programm per Rename
ersetzen, altes als `.old`; mac-app: DMG nach `~/Downloads` + `open`; docker/dev: nichts).
Aufbau in `server.NewUpdater`, gestartet von `serve` und der Desktop-App. Mit einer als
0.7.0 gebauten Testkopie echt durchgespielt: v0.7.1 gefunden, geladen, geprüft, ersetzt.
Release-Versionen tragen kein „v“ (`0.7.1`) – `update.Newer` nimmt beides. **R-032:**
Desktop-Builds bekamen keine Version; `release.yml` geändert, wirkt erst beim nächsten
Release (dann prüfen, ob DMG/Windows-App ihre Version zeigen und `.sha256` dabei sind).
Release `v0.8.0`: Release-Lauf (alle vier Jobs), Prüflauf (docker, builds, windows) und CI
grün; R-032 im Lauf bestätigt.

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
| F-006 | Eigene, lokale Issues für eigene Projekte (nicht nur GitHub spiegeln)? | Später; erst spiegeln (T-9.1) | entschieden | **Ja** – lokale Issues → D-028 (T-9.13) |
| F-007 | Code-Volltextsuche über alle Repos (z. B. zoekt)? | Phase 9 | entschieden | **git grep, ohne Index** → D-021 |
| F-008 | Standard-Port der Weboberfläche | z. B. 7878 (unbelegt prüfen) | entschieden | **7373** (7878 ist Radarr) → D-016 |
| F-009 | Pushes auf eigene Projekte automatisch an GitHub weiterreichen? | Später, optional pro Repo | entschieden | **Nur wenn je Repo eingeschaltet** → D-029 (T-9.14) |
| F-010 | Windows: portables Git mitliefern oder Installation voraussetzen? | Voraussetzen + Prüfung mit Hinweis; mitliefern später | entschieden | **Portables Git mitliefern** → D-020 |
| F-011 | Projektordner heißt `Portable RepoShelf ` (Leerzeichen am Ende) – umbenennen? | Ja, auf `Portable RepoShelf` oder `reposhelf` | entschieden | **Ja, ohne Leerzeichen** → D-030 |
| F-012 | Forgejo-Code übernehmen erlauben (Folge von F-003, bisher durch D-002 verboten)? | Erlauben, da RepoShelf selbst GPL ist | entschieden | **Ja** → D-015 (ersetzt D-002) |
| F-013 | Wie wird ein Host mit Port (`gitea.local:3000`, z. B. Gitea auf Unraid) im Ordnerpfad abgebildet? `:` ist unter Windows im Dateinamen verboten; bis zur Entscheidung lehnt die Prüfung solche Hosts ab | `source.host` bleibt `gitea.local:3000`, Ordner heißt `gitea.local+3000` (`+` kommt in Hostnamen nie vor); IPv6-Adressen ebenso ohne `[]` und `:` abbilden | entschieden | **Ordner ohne Port** (`gitea.local`), Doppelte werden gemeldet → D-018 |
| F-014 | Der Instanzschlüssel für den Abgleich (Ed25519, `.reposhelf/instance.key`, Rechte 0600) liegt im Datenordner. Konzept 4.2 sagt „Geheimnisse nie im Datenordner im Klartext“. So lassen? | So lassen: Er ist ein selbst erzeugter Maschinenschlüssel wie ein SSH-Hostkey, verlässt die Instanz nie und macht den portablen Betrieb (USB, Docker-Volume) erst möglich. Alternative: auf dem Desktop im Schlüsselbund, in Docker als Secret-Datei | entschieden | **So lassen** (Datei, 0600) → D-022 |
| F-015 | GitHub-Zugang am Desktop: Token selbst anlegen und eingeben, oder „Mit GitHub anmelden“ (Device-Flow: RepoShelf zeigt einen Code, du bestätigst auf github.com)? Letzteres braucht eine einmal registrierte OAuth-App (Client-ID, kein Geheimnis) | Beides: Token eingeben (T-9.10) sofort; Anmelden mit GitHub danach, wenn du die OAuth-App anlegen möchtest | entschieden | **Beides, Token zuerst** → D-024 |
| F-016 | Laufzeit-Daten (T-9.11): Ab welcher Größe fragt RepoShelf vor dem Herunterladen nach? | Je Eintrag ab 1 GB nachfragen; globale Obergrenze wie `retention.max_asset_mb` | entschieden | **Ab 1 GB je Eintrag** → D-025 |
| F-017 | Python-Pakete (z. B. PyTorch) für welche Plattformen sichern? | Nur die gewählten Plattformen je Repo (Standard: die eigene + linux/amd64 für Unraid); CUDA-Variante nur auf Wunsch | entschieden | **Je Repo wählbar** (Standard: eigene + linux/amd64) → D-026 |
| F-018 | Modelle aktualisieren oder einfrieren? | Wie Repos: festgehaltene Revision bleibt; neue Revisionen werden zusätzlich gesichert, ältere schlägt die Lösch-Prüfung vor | entschieden | **Wie Repos** → D-027 |
| F-019 | Welche der Funktionsvorschläge V-1 bis V-18 aus `docs/PRUEFBERICHT-2026-11.md` sollen umgesetzt werden (und in welcher Reihenfolge)? | Zuerst V-1 (Integritätsprüfung), V-2 (Benachrichtigungen), V-4 (README-Bilder mitsichern), V-5 (Repo wiederbeleben) | entschieden | **V-1, V-2, V-4, V-5** → D-034 (T-9.21–T-9.24); später **V-14, V-3, V-6, V-15** → D-036 (T-9.29–T-9.32) |

---

## Review-Befunde

Für Prüfungen durch Codex, Claude Code oder Dennis. Befunde werden hier
eingetragen, nicht still im Code geändert (außer der Prüfer hat den Auftrag
dazu). Schwere: `kritisch` · `hoch` · `mittel` · `gering` · `Hinweis`.

| ID | Befund | Fundstelle | Schwere | Status | Gefunden von / Datum | Bezug |
|---|---|---|---|---|---|---|
| R-001 | Ordner-Übernahme prüft nicht, ob der Ordner den Datenordner enthält oder darin liegt: das Archiv würde in sich selbst kopiert, mit „Verschieben“ käme der Datenordner in den Papierkorb | `internal/core/importdir.go` InspectFolder | hoch | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-002 | Ordner-Übernahme läuft im Web-Request: große Modelle blockieren minutenlang ohne Fortschritt (widerspricht T-9.9) | `internal/core/importdir.go` ImportFolder, `internal/web/handlers.go` importFolder | mittel | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-003 | Vorschau durchläuft beliebig große Ordner (z. B. `C:\`) bei jeder Eingabe | `internal/core/importdir.go` folderFiles | mittel | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-004 | Modell-Ordner für Programme scheitert auf Datenträgern ohne Hardlinks (exFAT/FAT – portabler USB-Betrieb) bei jedem Abgleich | `internal/core/hfhome.go` exportHF | mittel | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-005 | Ausstehende Änderungen an einem schon gesendeten lokalen Issue (L3 → #12) werden nirgends angezeigt | `internal/core/extras.go` Issues | gering | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-006 | SendIssues hält eine globale Sperre während der Netzaufrufe: Issue-Änderungen aller Repos warten | `internal/core/issues.go` issueMu | gering | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-007 | PutLFS ersetzt ein vorhandenes Objekt per Rename; unter Windows scheitert das am schreibgeschützten Ziel | `internal/gitcli/commitfiles.go` PutLFS | gering | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-008 | macOS: Papierkorb per Umbenennen ohne „Zurücklegen“; `/usr/bin/trash` (ab macOS 15) nutzen | `internal/systrash/trash_darwin.go` | Hinweis | erledigt `9364dc0` | Claude Code · 2026-11-11 | T-9.16–T-9.18 |
| R-009 | Kapsel: Der Build-Container (root) legt `.reposhelf-capsule/` in der Arbeitskopie an; das Toolchain-Image konnte danach dort nicht abgelegt werden (Python, Prüflauf v0.5.1) | `internal/runner/capsule.go` saveToolchain | mittel | erledigt `51de8ce` | Prüflauf `smoke.yml` · 2026-11-11 | T-9.12 |
| R-010 | Nach jedem Push schreibt `refreshStats` `repo.yaml` neu, obwohl sich nur Statistik (upstream.json) ändert – eine gleichzeitige Handänderung geht verloren | `internal/core/sync.go` refreshStats | gering | erledigt `51de8ce` | Claude Code · 2026-11-11 | Invariante 6 |
| R-011 | Lokales Vertrauen ohne Prüfung des Host-Kopfs: per DNS-Rebinding handelt eine fremde Webseite als lokaler Verwalter (alles lesen, Tokens anlegen, lokale Ordner übernehmen, Builds starten) | `internal/server/server.go` isLocal/resolve; `internal/web/web.go` sameOrigin | hoch | erledigt `0d67fac` | Claude Code · 2026-11-11 | Prüfbericht |
| R-012 | REST-API prüft weder Herkunft noch Inhaltstyp: fremde Seiten können für den lokal vertrauten Nutzer Repos aufnehmen und Abgleiche starten (CSRF) | `internal/api/api.go` POST /api/repos, /api/sync | mittel | erledigt `0d67fac` | Claude Code · 2026-11-11 | Prüfbericht |
| R-013 | Das Build-Ziel im (fremden) Repo bestimmt, welche `REPOSHELF_SECRET_*` in den Container gehen – Geheimnisse an fremden Code mit Netzzugang | `internal/runner/exec.go:83` | hoch | erledigt `8b0f428` (`buildable.secrets`) | Claude Code · 2026-11-11 | Prüfbericht |
| R-014 | `act` bindet standardmäßig den Docker-Anschluss ein, Workflows dürfen Container-Optionen/Laufwerke setzen: fremder Workflow kann den Host-Docker steuern (D-008); Annahme gegen act 0.2.97 bestätigen | `internal/runner/exec.go` runAct | hoch | erledigt `395ecd2` | Claude Code · 2026-11-11 | Prüfbericht |
| R-015 | Artefakt-Muster folgen Verweisen, die der Build-Container anlegt: Dateien des Hosts landen in den Artefakten | `internal/runner/exec.go` collect | mittel | erledigt `ce50a4c` | Claude Code · 2026-11-11 | Prüfbericht |
| R-016 | Submodule aus `.gitmodules` werden ungefragt und rekursiv als Spiegel aufgenommen (bis 20 je Abgleich): fremde Daten lösen Downloads aus, auch ins Heimnetz; Platte kann volllaufen | `internal/core/extras.go` syncSubmodules | mittel | erledigt `331c1a4` | Claude Code · 2026-11-11 | Prüfbericht |
| R-017 | Go 1.27.3 hat GO-2026-6859 (Absturz des HTTP/1-Servers), behoben in 1.27.4; `govulncheck` fehlt in der CI | `go.mod`, `.github/workflows/ci.yml` | mittel | erledigt `7e34e1a` | Claude Code · 2026-11-11 | Prüfbericht |
| R-018 | Kein Schutz gegen Durchprobieren von Tokens bzw. des frei gewählten Admin-Tokens | `internal/server/server.go` login/resolve | gering | erledigt `f949985` | Claude Code · 2026-11-11 | Prüfbericht |
| R-019 | Weiterleitung nach Anmeldung: `next=/\evil.example` besteht die Prüfung (Browser lesen `/\` wie `//`) | `internal/server/server.go` login | gering | erledigt `f949985` | Claude Code · 2026-11-11 | Prüfbericht |
| R-020 | Sitzungen: abgelaufene werden nie entfernt; Cookie hinter HTTPS-Proxy ohne „Secure“ | `internal/server/server.go` | gering | erledigt `f949985` | Claude Code · 2026-11-11 | Prüfbericht |
| R-021 | Kopplung: 40-Bit-Code mit mitgeschicktem HMAC-Nachweis ist durchprobierbar (Umleiten im LAN); signierte Anfragen ohne Einmalnummer wiederholbar | `internal/peer/peer.go` | gering | erledigt `a60a1fe` | Claude Code · 2026-11-11 | Prüfbericht |
| R-022 | Geheimnisse als `-e NAME=wert` in der Prozessliste; Image-Namen aus Repo-Dateien ungeprüft an docker (führendes `-`) | `internal/runner/exec.go`, `capsule.go` | gering | erledigt `ccc00ea` | Claude Code · 2026-11-11 | Prüfbericht |
| R-023 | Datenschutz: README-Bilder werden von fremden Servern geladen (IP-Adresse geht an Dritte; Bilder fehlen offline) | `internal/web/web.go` CSP img-src | gering | erledigt `0f2b94e` (mit T-9.23) | Claude Code · 2026-11-11 | Prüfbericht |
| R-024 | Git-Härtung fehlt: `fetch.fsckObjects`, `GIT_ALLOW_PROTOCOL`, Einfluss der globalen Git-Konfiguration; „dubious ownership“ auf FAT/exFAT prüfen (portabel) | `internal/gitcli/gitcli.go` | Hinweis | erledigt `4f01614` (Protokoll-Liste, safe.directory; `fetch.fsckObjects` bewusst nicht – bricht Spiegel alter Repos, siehe F-019/V-1) | Claude Code · 2026-11-11 | Prüfbericht |
| R-025 | Dateinamen aus Repos ungequotet in `Content-Disposition` | `internal/web/handlers.go` repoRaw, ServeFileSafe | Hinweis | erledigt `ccc00ea` | Claude Code · 2026-11-11 | Prüfbericht |
| R-026 | `activity.jsonl` wächst unbegrenzt und wird je Abruf ganz gelesen | `internal/core/events.go` RecentActivity | gering | erledigt `4f01614` | Claude Code · 2026-11-11 | Prüfbericht |
| R-027 | Laufzeit-Image-Bau ohne Ressourcengrenze; ZIP-Import mit `.git` holt per `git fetch` aus fremdem Ordner | `internal/runner/image.go`, `internal/core/zipimport.go` | Hinweis | erledigt `4f01614` (ZIP neutralisiert; Image-Bau ohne Grenze dokumentiert) | Claude Code · 2026-11-11 | Prüfbericht |
| R-028 | README wird nur im Hauptordner gesucht; GitHub zeigt auch `.github/README.md` und `docs/README.md` (z. B. sherlock-project/sherlock: „keine README“) | `internal/gitcli/read.go` Readme | mittel | erledigt `6a54ad9` (Reihenfolge wie GitHub: `.github/`, Hauptordner, `docs/`) | Dennis · 2026-11-22 | Test v0.6.0 |
| R-029 | HTML in READMEs (`<img>`, `<p align>`, `<details>`) wird ganz verworfen: Logos und Bilder fehlen, obwohl GitHub sie zeigt | `internal/web/web.go` renderMarkdown, `internal/core/readmeassets.go` | mittel | erledigt `6a54ad9` (HTML wird gerendert und von `internal/htmlsafe` auf eine Positivliste gefiltert; `<img>`-Bilder werden mitgesichert) | Dennis · 2026-11-22 | Test v0.6.0 |
| R-030 | `golang.org/x/net` v0.60.0 hat GO-2026-6860 (quadratischer Aufwand im HTML-Parser); seit R-029 parst `internal/htmlsafe` fremde READMEs damit – präparierte README bremst Seite und Abgleich aus | `go.mod`, `internal/htmlsafe/htmlsafe.go:90` | mittel | erledigt `185f458` (x/net v0.62.0) | CI govulncheck · 2026-11-22 | R-029, v0.6.1 |
| R-031 | `/static/` wird eine Stunde zwischengespeichert (`public, max-age=3600`), ohne Versionskennung in der Adresse: nach einem Update zeigt der Browser bis zu einer Stunde alte Styles/Skripte zur neuen Seite (bei T-9.25 gesehen: Hinweisblöcke ohne Farbe) | `internal/web/web.go:166` | niedrig | erledigt `e92f2a6` (Seiten verlinken `/static/…?v=<Inhalts-Hash>`, dauerhaft zwischengespeichert; ohne passende Version `no-cache` mit ETag → 304) | Claude Code · 2026-11-24 | T-9.25 |
| R-032 | Desktop-Apps (DMG, Windows-ZIP) werden ohne Versionsnummer gebaut und zeigen „dev“; DMG und Windows-ZIP fehlen in `SHA256SUMS` | `.github/workflows/release.yml` (wails build) | niedrig | erledigt `7c6adc8` (`-ldflags` mit Version, eigene `.sha256`-Dateien; in `v0.8.0` geprüft: LDFlags `Version=0.8.0`, Prüfsummen = GitHub-Digests) | Claude Code · 2026-11-25 | T-9.32 |

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
| T-9.7 | GitHub-Actions-Artefakte mitsichern, Anzahl der Läufe einstellbar (global/Sammlung/Repo) | erledigt | Claude Code | 2026-11-08 | Wunsch von Dennis beim Test von v0.1.5; `include.actions`, `retention.actions_keep` (SCHEMA ergänzt) |
| T-9.8 | Windows: kein Konsolenfenster bei Git- und Docker-Aufrufen | erledigt | Claude Code | 2026-11-10 | Rückmeldung Dennis zu v0.1.7; e196256 |
| T-9.9 | Seitenwechsel beschleunigen; lange Arbeiten im Hintergrund mit Fortschrittsbalken | erledigt | Claude Code | 2026-11-10 | Rückmeldung Dennis zu v0.1.7; d59ea17; Test `TestPageGitCost` |
| T-9.10 | Host-Tokens am Desktop: Windows-Anmeldeinformationsverwaltung lesen (Konzept 4.2 nennt den System-Schlüsselbund, bisher nur macOS/Linux) und Token in der Oberfläche eingeben, prüfen (Rechte anzeigen) und im Schlüsselbund ablegen – nie im Datenordner | erledigt | Claude Code | 2026-11-10 | D-024; `48c4c28` – Einstellungen „Zugänge zu Git-Hosts“, `reposhelf host-token`, Schlüsselbund macOS/Windows/Linux; „Mit GitHub anmelden“ folgt, wenn die OAuth-App angelegt ist |
| T-9.11 | Laufzeit-Daten sichern: Modelle (Hugging Face als eigenes gespiegeltes Repo mit LFS, gemeinsamer Speicher, Bereitstellung als `HF_HOME`-Ordner für den Offline-Betrieb), Verweis „braucht“ vom Tool auf das Modell, Vorschläge aus dem Code (`from_pretrained`, `hf_hub_download`); Python-Pakete mit genauer Versionsliste für gewählte Plattformen; optional Laufzeit-Image (`docker save`) | erledigt | Claude Code | 2026-11-11 | Schritt 1 `60f8718` (HF-Modelle, Nachfrage D-025, Erkennung im Code); `a6025c7` HF_HOME-Ordner `runtime/huggingface` (Hardlinks, schreibgeschützt); `e0232f7` Python-Pakete je Plattform + CPU-PyTorch (D-026). Optionales Laufzeit-Image → T-9.19 |
| T-9.12 | Build-Kapsel: Toolchain-Image (z. B. `golang:1.24`, `node:20`) mit `docker save` in den gemeinsamen Speicher, damit der Kapsel-Test nicht vom Docker Hub abhängt | erledigt | Claude Code | 2026-11-10 | `a96a9d0` – Toolchain-Image per `docker save` in den gemeinsamen Speicher (einmal je Image-ID), Kapsel-Test lädt es bei Bedarf |
| T-9.13 | Lokale Issues: anlegen, kommentieren, schließen, wieder öffnen – Oberfläche, CLI, MCP (Recht schreiben); gespiegelte Issues bleiben schreibgeschützt | erledigt | Claude Code | 2026-11-10 | D-028; `e4bc0de` – Reiter „Issues“ immer da, `reposhelf issue`, MCP `list_issues`/`create_issue`/`comment_issue`/`set_issue_state` |
| T-9.14 | Pushes auf eigene Projekte/Forks an das Original weiterreichen, je Repo einschaltbar (Standard aus), ohne Force-Push | erledigt | Claude Code | 2026-11-10 | D-029; `8c60f60` – `sync.push_upstream` (SCHEMA ergänzt), nie erzwungen, Fehler am Repo |
| T-9.15 | Projektordner ohne Leerzeichen am Ende | erledigt | Claude Code | 2026-11-10 | D-030 – Ordner heißt jetzt `Portable RepoShelf` (am Sitzungsende umbenannt; `.claude/launch.json` angepasst) |
| T-9.16 | Issues eigener Projekte/Forks lokal bearbeiten (auch gespiegelte), Schalter je Repo „Issues an das Original senden“, ausstehende Änderungen markieren und per Knopf senden (GitHub, Gitea/Forgejo) | erledigt | Claude Code | 2026-11-11 | D-031; `86df0dd` – `sync.issues_upstream`, `issues/outbox.yaml`, `reposhelf issue send`; GitHub + Gitea/Forgejo (GitLab noch nicht) |
| T-9.17 | Lokale Ordner übernehmen (Modelle): Pfad im Hinzufügen-Feld, HF-Cache erkennen oder Modell-ID abfragen, vorhandene Dateien statt Download, Kopieren/Verschieben (Original in System-Papierkorb) | erledigt | Claude Code | 2026-11-11 | D-032, D-033; `697b589` – Pfad im Hinzufügen-Feld/`add <ordner>`, HF-Cache-Erkennung, LFS-Vorbefüllung, Prüfung nach erstem Abgleich, Papierkorb über `internal/systrash` |
| T-9.18 | LFS-Dateien über Git-HTTP ausliefern (LFS-Batch-API), damit `git clone` über RepoShelf Modelle und große Dateien vollständig bekommt | erledigt | Claude Code | 2026-11-11 | `5a509bc` – LFS-Batch-API unter `…/info/lfs/` (Download, Upload für eigene Repos, locks/verify); Prüflauf klont ein HF-Modell über RepoShelf |
| T-9.19 | Optional: Laufzeit-Image je Repo (fertige Umgebung per `docker save`, z. B. für Dienste auf Unraid) | erledigt | Claude Code | 2026-11-11 | aus T-9.11 abgetrennt; `64316c4` – Build-Ziel `image` (docker build + save → `.tar.gz` im Archiv), „In Docker laden“ |
| T-9.20 | Issues (gespiegelte und lokale) in die Volltextsuche aufnehmen (KONZEPT 6.9) | erledigt | Claude Code | 2026-11-11 | Abgleich Konzept ↔ Stand; `ddf1416` – FTS-Spalte `issues` (Index-Schema 2, wird neu aufgebaut); KONZEPT.md an D-021–D-033 angeglichen |
| T-9.21 | V-1 Integritätsprüfung nach Zeitplan: `git fsck`, Prüfsummen von Release-Dateien, LFS-Objekten und Kapsel-Blobs; Bericht, Warnung bei Schäden | erledigt | Claude Code | 2026-11-21 | D-034; `068b98c` – `retention.integrity_every` (30d), Einstellungen → Integrität, `reposhelf check` |
| T-9.22 | V-2 Benachrichtigungen: neue Version, Original weg/archiviert, Force-Push, Abgleich dauerhaft fehlgeschlagen, Integritätsschaden – Meldungen in der Oberfläche, Webhook (ntfy, Discord, allgemein), Desktop-Meldung | erledigt | Claude Code | 2026-11-21 | D-034; `8a94f26` – Meldungen-Seite, Webhook ntfy/Discord/JSON (Adresse im Schlüsselbund bzw. `REPOSHELF_NOTIFY_URL`); Betriebssystem-Meldungen der Desktop-App noch nicht |
| T-9.23 | V-4 README-Bilder beim Abgleich mitsichern und lokal ausliefern; fremde Bilder sonst nicht laden (behebt R-023) | erledigt | Claude Code | 2026-11-21 | D-034; `0f2b94e` – `meta/…/readme-assets/`, CSP ohne fremde Bilder |
| T-9.24 | V-5 Repo wiederbeleben: Spiegel mit einem Klick zu GitHub oder Gitea/Forgejo hochladen (Repo anlegen, alle Branches/Tags, Releases mit Dateien) | erledigt | Claude Code | 2026-11-21 | D-034; `fefc576` – Repo → Einstellungen → Wiederbeleben (nur Verwalter) |
| T-9.25 | GitHub-Hinweisblöcke in READMEs (`> [!NOTE]`, `> [!WARNING]` …) als farbige Hinweise darstellen statt als Zitat mit `[!WARNING]` | erledigt | Claude Code | 2026-11-24 | `2e7c68b` (aufgefallen bei R-029, sherlock) |
| T-9.26 | Release-Dateien nach Betriebssystem, Prozessor und Format auswählen (Erkennung am Dateinamen, Begleitdateien folgen, Muster); nur passende Dateien laden | erledigt | Claude Code | 2026-11-22 | D-035; `65b3053` – `internal/assetclass` |
| T-9.27 | Release-Profile in den Einstellungen (Standard fest, Vorgabe wählbar); beim Hinzufügen Profil, Anzahl Versionen und bestimmte Versionen (angeheftet) wählen, mit Größenschätzung | erledigt | Claude Code | 2026-11-22 | D-035; `65b3053` – Einstellungen → Release-Profile; CLI `add --profile --keep --pin` |
| T-9.28 | Dialog nach Änderung der Auswahl: nicht mehr passende Dateien in den Papierkorb oder Auswahl anpassen; sonst Vorschlag unter „Aufräumen“ | erledigt | Claude Code | 2026-11-22 | D-035; `65b3053` – Repo → Einstellungen → Release-Dateien |
| T-9.29 | V-14 Speicherplanung: Verlauf des Verbrauchs (gesamt, je Sammlung, freier Platz), Vorhersage, Obergrenze je Sammlung mit Warnung | erledigt | Claude Code | 2026-11-25 | D-036; `6dc3120` – Seite „Speicher“, `.reposhelf/usage.jsonl`, `max_size_mb` in Sammlungen |
| T-9.30 | V-3 „Was ist neu“ oben auf der Bibliothek: neue Releases/Commits seit dem letzten Besuch, Problemfälle, Speicher | erledigt | Claude Code | 2026-11-25 | D-036; `ec86975` – `Activity.Changes`, `core.News`, Cookies `rs_since`/`rs_last` |
| T-9.31 | V-6 Commits und Vergleich: Verlauf blättern, Änderung eines Commits, zwei Versionen vergleichen | erledigt | Claude Code | 2026-11-25 | D-036; `277d512` – Reiter „Commits“, `/commit/{rev}`, `/compare` |
| T-9.32 | V-15 Selbst-Aktualisierung: Prüfung auf neue Version (abschaltbar), portable Programme ersetzen sich nach SHA256-Prüfung, Mac-App lädt DMG, Docker nur Hinweis | erledigt | Claude Code | 2026-11-25 | D-036; `7c6adc8` – Paket `internal/update`, Karte „Aktualisierung“, `updates.check` |

---

## Verlauf

Neueste oben. Eine Zeile pro Sitzung.

- 2026-11-25 · Claude Code · Release `v0.8.0` mit T-9.29–T-9.32, Prüflauf und CI grün, R-032 bestätigt
- 2026-11-25 · Claude Code · T-9.32 Selbst-Aktualisierung; R-032 Desktop-Builds ohne Version (behoben in `release.yml`)
- 2026-11-25 · Claude Code · T-9.31 Commits, Commit-Ansicht und Vergleich zweier Stände
- 2026-11-25 · Claude Code · T-9.30 „Was ist neu“ auf der Bibliothek
- 2026-11-25 · Claude Code · T-9.29 Speicherplanung (Verlauf, Vorhersage, Obergrenze je Sammlung); D-036, T-9.30–T-9.32 eingetragen
- 2026-11-25 · Claude Code · Release `v0.7.1` mit T-9.25 und R-031, Prüflauf und CI grün
- 2026-11-25 · Claude Code · R-031 versionierte Adressen für `/static/`, Browser bekommt neue Styles sofort nach Update
- 2026-11-24 · Claude Code · T-9.25 GitHub-Hinweisblöcke in READMEs; R-031 eingetragen (Zwischenspeicher `/static/`)
- 2026-11-22 · Claude Code · Release `v0.7.0` mit T-9.26–T-9.28, Prüflauf und CI grün
- 2026-11-22 · Claude Code · T-9.26–T-9.28 Release-Dateien nach Plattform, Release-Profile, Dialog für nicht mehr passende Dateien (D-035)
- 2026-11-22 · Claude Code · Release `v0.6.2` mit R-030, Prüflauf und CI grün
- 2026-11-22 · Claude Code · Release `v0.6.1` (Prüflauf grün); R-030 `x/net` v0.62.0 (govulncheck)
- 2026-11-22 · Claude Code · R-028 README in `docs/`/`.github/`, R-029 HTML in READMEs gefiltert darstellen (`internal/htmlsafe`); T-9.25 eingetragen
- 2026-11-21 · Claude Code · T-9.21 Integritätsprüfung, T-9.22 Benachrichtigungen, T-9.23 README-Bilder mitsichern (R-023), T-9.24 Wiederbeleben; Release `v0.6.0`, Prüflauf und CI grün

- 2026-11-21 · Dennis · F-019 entschieden: V-1, V-2, V-4, V-5 (D-034) → T-9.21–T-9.24

- 2026-11-21 · Claude Code · R-015–R-022, R-024–R-027 behoben (Artefakte, Submodule, Go 1.27.4 + govulncheck, Fehlversuche, Weiterleitung, Sitzungen, Kopplung, Image-Namen, Git-Härtung, Dateinamen, Protokoll, ZIP); Release `v0.5.4`, Prüflauf und CI grün (govulncheck: keine Schwachstellen)

- 2026-11-21 · Claude Code · R-011–R-014 behoben (DNS-Rebinding, API-CSRF, Geheimnisse nur nach Freigabe, act abgedichtet); Release `v0.5.3` (nach Klärung der GitHub-Abrechnung), Prüflauf grün inkl. `act` ohne Docker-Socket

- 2026-11-11 · Claude Code · Prüfung des Projekts (Sicherheit, Probleme, Optimierungen, fehlende Funktionen): `docs/PRUEFBERICHT-2026-11.md`, Befunde R-011–R-027, Vorschläge in F-019; kein Code geändert

- 2026-11-11 · Claude Code · Prüflauf v0.5.1: Python-Kapsel scheiterte am root-eigenen Kapselordner (R-009); dabei R-010 (Push schrieb repo.yaml neu) gefunden; beides behoben; Release `v0.5.2`, Prüflauf grün (Python-Kapsel mit echten Windows-Paketen)

- 2026-11-11 · Claude Code · Eigene Prüfung: R-001–R-008 (u. a. Datenordner als Quelle gesperrt, Übernahme im Hintergrund, exFAT) behoben; T-9.20 Issues in der Volltextsuche; KONZEPT angeglichen; Release `v0.5.1`

- 2026-11-11 · Claude Code · T-9.19 Laufzeit-Image (Build-Ziel `image`, Laden in Docker); alle Aufgaben erledigt; Release `v0.5.0`, Prüflauf grün (echtes Image gebaut, in Docker geladen, gestartet)

- 2026-11-11 · Claude Code · T-9.18 LFS über Git-HTTP; T-9.11 fertig (HF_HOME-Ordner, Python-Pakete je Plattform, CPU-PyTorch); T-9.19 (Laufzeit-Image) als optional abgetrennt; Release `v0.4.0`, Prüflauf grün (HF-Modell per `git-lfs` über RepoShelf geklont, Modell-Ordner im Container geprüft)

- 2026-11-11 · Claude Code · Prüflauf v0.3.0: Windows-Papierkorb griff nicht (Pfad mit Leerzeichen als PowerShell-Code-Text zerlegt, auch Einschleusung möglich) → Pfad per Umgebungsvariable; Release `v0.3.1`, Prüflauf grün

- 2026-11-11 · Claude Code · T-9.16 Issues eigener Repos lokal bearbeiten/senden (D-031), T-9.17 lokale Modell-Ordner übernehmen (D-032, D-033); neu T-9.18 (LFS über Git-HTTP); Release `v0.3.0`

- 2026-11-11 · Dennis · Wünsche: Issues eigener Repos lokal bearbeiten und auf Wunsch senden (D-031); lokale Modell-Ordner übernehmen, Kopieren/Verschieben (D-032, D-033) → T-9.16, T-9.17

- 2026-11-10 · Claude Code · T-9.12 Toolchain-Image in Kapseln, T-9.13 lokale Issues, T-9.14 Push-Weiterreichen (opt-in), T-9.11 Schritt 1 Hugging-Face-Modelle; Release `v0.2.0`, Prüflauf grün (Toolchain-Image `golang:1.24` echt gesichert, 319 MB); Projektordner ohne Leerzeichen (T-9.15)

- 2026-11-10 · Dennis · v0.1.10 unter Windows getestet und abgenommen: keine Konsolenfenster, Seitenwechsel flott, Fortschrittsbalken läuft, privates Repo lässt sich mit Token sichern; F-006/F-009/F-011 entschieden (D-028–D-030)

- 2026-11-10 · Claude Code · Dennis' Test v0.1.9: eigenes privates Repo „nicht gefunden“ trotz Token (Token ohne Zugriff auf das Repo) → Meldung nennt jetzt Token-Herkunft und was auf GitHub freizugeben ist; Hinweis, wenn eine Umgebungsvariable den Schlüsselbund überdeckt; Release `v0.1.10`

- 2026-11-10 · Claude Code · T-9.10: Tokens in den Einstellungen eingeben/prüfen/entfernen, Schlüsselbund auch unter Windows, `reposhelf host-token`, Hinweis bei privaten Repos ohne Token; Release `v0.1.9`, Prüflauf grün (auch Windows-Anmeldeinformationsverwaltung)

- 2026-11-10 · Dennis · F-015–F-018 entschieden (D-024 Token zuerst, D-025 Nachfrage ab 1 GB, D-026 Plattformen je Repo, D-027 Modelle wie Repos)

- 2026-11-10 · Claude Code · Neue Aufgaben T-9.10 (Tokens am Desktop/Windows), T-9.11 (Laufzeit-Daten: Modelle, Pakete, Image), T-9.12 (Toolchain-Image in Kapsel); Fragen F-015–F-018 an Dennis

- 2026-11-10 · Claude Code · Dennis' Rückmeldung v0.1.7 (Windows): Konsolenfenster blitzten auf, Seitenwechsel langsam → T-9.8 (kein Konsolenfenster), T-9.9 (Git-Aufrufe je Seite auf 0, Fortschrittsbalken, Ladebalken); Release `v0.1.8`, Prüflauf `smoke.yml` grün (Docker, Windows, Builds)

- 2026-11-08 · Dennis · v0.1.7 getestet: Ordner öffnet, Hinzufügen springt zum Repo; Notarisierung bleibt weg (D-023)
- 2026-11-08 · Claude Code · Dennis' Test v0.1.6: Datenordner `…/# Meine Apps/App-Vault` ließ sich nicht öffnen – `#` im Pfad beendete den SQLite-Dateinamen (URI) → Pfad wird jetzt maskiert, Test mit Sonderzeichen; Release `v0.1.7`
- 2026-11-08 · Claude Code · v0.1.6 Prüflauf grün; Actions-Artefakt-Download live gegen GitHub geprüft (SoloSidian)
- 2026-11-08 · Claude Code · Dennis' Test v0.1.5: Fenster ok, aber nach „Hinzufügen“ keine Weiterleitung (WKWebView folgt 303 im Wails-Schema nicht) → behoben; T-9.7 Actions-Artefakte; Release `v0.1.6`
- 2026-11-08 · Claude Code · Dennis' Test: Desktop-Fenster blieb weiß (Wails lieferte den Platzhalter `frontend/dist/index.html` statt der Oberfläche) → behoben, Fenster-Test (`cmd/reposhelf-desktop/window_test.go`) auch im Release-Job; Klon-Adresse im Fenster korrigiert; Release `v0.1.5`
- 2026-11-08 · Claude Code · Echte Builds in CI geprüft (Docker-Ziel, Kapsel, act, GitHub Actions); Fehler behoben: git ohne Repo lief im Arbeitsordner (doppelter Auth-Header); Einrichtungsdialog testbar (`internal/desktop`); macOS ad-hoc signiert; Release `v0.1.4`
- 2026-11-08 · Claude Code · Prüflauf für `v0.1.3` grün (Docker, Windows-CLI, Windows-Desktop); macOS-Desktop lokal geprüft
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
