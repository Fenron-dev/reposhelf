# RepoShelf – Entscheidungen

> Getroffene Grundsatzentscheidungen mit Begründung. Eine Entscheidung wird
> **nicht gelöscht oder still geändert**: Wird sie revidiert, bekommt sie den
> Status `ersetzt durch D-0xx` und es entsteht ein neuer Eintrag.
> Neue Entscheidungen trifft Dennis; Agenten tragen sie nach seiner Zustimmung ein.

Format: **ID · Titel** – Datum · Status – Entscheidung – Begründung – Folgen

---

### D-001 · Go als Programmiersprache
2026-11-08 · gültig

**Entscheidung:** RepoShelf wird in Go geschrieben.

**Begründung:** Gitea ist in Go geschrieben und steht unter MIT-Lizenz. Code
lässt sich mit Lizenzhinweis direkt übernehmen. Go kompiliert schnell und mit
kleinem Cache, RepoShelf ist also auch auf dem MacBook mit wenig Speicher
lokal entwickelbar. Eine statische Binärdatei passt zu „portabel“ und Docker.
Rust wurde erwogen (Fero-Code), aber die übertragbaren Fero-Teile sind klein
und als Konzepte leicht nachzubauen.

**Folgen:** Wails statt Tauri für die Desktop-Hülle. Fero/MediaShelf liefern
Konzepte, keinen Code.

### D-002 · Eigene Software; Gitea als Code-Quelle, Forgejo nur als Ideengeber
2026-11-08 · ersetzt durch D-015

**Entscheidung:** RepoShelf hängt nicht von Gitea/Forgejo als laufendem
Programm ab. Code aus Gitea (MIT) darf übernommen werden, Code aus Forgejo
(GPL v3+ seit v9.0) nicht.

**Begründung:** Volle Kontrolle; Forgejo-Code würde RepoShelf GPL-pflichtig machen.

**Folgen:** Herkunftskommentar + `THIRD_PARTY.md` für jede Übernahme (T-0.6).

### D-003 · Ein Programm, zwei Gesichter
2026-11-08 · gültig

**Entscheidung:** Server (Docker/Unraid) und Desktop sind dieselbe Binärdatei.
Der Desktop startet den Server intern und zeigt die Weboberfläche in einem
nativen Fenster (Wails). Auch die Runner-Rolle steckt in derselben Binärdatei.

**Begründung:** Eine Codebasis, kein doppelter Bau („Nichts doppelt bauen“,
Fundus2). Ein starker PC kann jederzeit Runner werden.

**Folgen:** Der Kern darf nichts von Wails importieren.

### D-004 · YAML ist die Wahrheit, SQLite ist der Index
2026-11-08 · gültig

**Entscheidung:** Metadaten liegen als YAML-Dateien (`meta/…/repo.yaml`).
SQLite enthält nur abgeleitete Daten und ist mit `reindex` neu aufbaubar.

**Begründung:** Von Hand bearbeitbar, mit Git versionierbar, für LLMs lesbar,
robust gegen kaputte Datenbank. Bewährt in MediaShelf.

**Folgen:** Unbekannte YAML-Felder werden beim Schreiben erhalten. Schreiben
immer atomar.

### D-005 · Archivschutz vor Aktualität
2026-11-08 · gültig

**Entscheidung:** Vor jedem Fetch, der Refs umschreiben oder löschen würde,
wird der bisherige Stand als Backup-Refs gesichert. Gelöscht wird nur über
den Papierkorb und nach Bestätigung. Angeheftetes ist von Regeln ausgenommen.

**Begründung:** Der Zweck von RepoShelf ist, dass nichts verloren geht, auch
wenn das Original verschwindet oder überschrieben wird.

### D-006 · System-`git` statt reiner Go-Implementierung
2026-11-08 · gültig

**Entscheidung:** Git-Operationen laufen über die `git`-CLI (plus `git-lfs`).
`go-git` höchstens für Lesezugriffe, wo es messbar hilft.

**Begründung:** Volle Funktion (Mirror, LFS, Submodule, `http-backend`),
bewährt (Gitea arbeitet ebenso).

**Folgen:** `git` muss vorhanden sein; `reposhelf doctor` prüft Version.
Windows: siehe F-010.

### D-007 · Dunkles Theme zuerst
2026-11-08 · gültig

**Entscheidung:** Die Oberfläche startet nur mit einem dunklen Theme (Entwurf
in `design/`). Themes folgen in T-9.3.

**Folgen:** Farben trotzdem von Anfang an als Variablen/Tokens anlegen, nicht
fest im Markup.

### D-008 · Builds nur in Containern auf Runnern
2026-11-08 · gültig

**Entscheidung:** Builds fremder und eigener Repos laufen isoliert in
Containern auf einem Runner. macOS/iOS-Builds über einen Mac-Runner oder
GitHub Actions als externen Runner.

**Begründung:** Sicherheit (fremder Code), Speicher (MacBook),
Reproduzierbarkeit. macOS-Builds sind unter Linux nicht möglich.

### D-009 · Sprache
2026-11-08 · gültig

**Entscheidung:** Oberfläche und Dokumentation auf Deutsch. Code, Bezeichner,
Code-Kommentare und Commit-Nachrichten auf Englisch.

### D-010 · Projektführung für mehrere Agenten
2026-11-08 · gültig

**Entscheidung:** `AGENTS.md` enthält die verbindlichen Regeln für alle Agenten
(Codex liest sie direkt). `CLAUDE.md` importiert `AGENTS.md` und ergänzt nur
Claude-spezifisches. Fortschritt steht ausschließlich in `docs/STATUS.md`.

**Begründung:** Eine Quelle, kein Auseinanderlaufen zweier Regelwerke.
Mehrere Agenten (Claude Code, Codex) arbeiten abwechselnd und brauchen eine
gemeinsame, eindeutige Übergabe.

### D-011 · MCP ohne Löschrechte
2026-11-08 · gültig

**Entscheidung:** Über MCP sind Lesen, Organisieren, Abgleich und Builds
möglich, aber kein Löschen, kein Papierkorb, keine Aufbewahrungsregeln.

**Begründung:** Zerstörende Aktionen bleiben beim Menschen; Schutz gegen
Fehlbedienung und Prompt-Injection über fremde Repo-Inhalte.

### D-012 · Repo privat, CI sparsam
2026-11-08 · gültig (beantwortet F-001)

**Entscheidung:** Das GitHub-Repo von RepoShelf ist privat. Konzept, Status
und Design bleiben in `docs/` bzw. `design/` im Repo (kein Auslagern in eine
ignorierte `Dokumentation/` wie bei Fero/Fundus2).

**Begründung:** Wunsch von Dennis. Die öffentlichen Projekte sind nur deshalb
öffentlich, weil private Repos bei GitHub Free begrenzte Actions-Minuten haben
(2.000 Min./Monat; Windows zählt 2-fach, macOS 10-fach) und Builds sonst
nicht mehr liefen.

**Folgen:**
- CI läuft auf Linux-Runnern. Go baut macOS- und Windows-Binärdateien ohne CGO
  per Cross-Compile auf Linux; dafür braucht es keinen macOS-Runner.
- macOS-Runner nur für die Wails-Desktop-Paketierung (DMG), und nur manuell
  oder bei einem `v*`-Tag.
- Selbst gehostete Runner kosten keine Minuten → Runner auf Unraid prüfen
  (T-0.10). Langfristig ist genau das ein Zweck von RepoShelf (Phase 7):
  eigene Runner, damit auch andere Projekte wieder privat sein können.

### D-013 · Repo-Name und Go-Modulpfad
2026-11-08 · gültig (beantwortet F-002)

**Entscheidung:** Das GitHub-Repo heißt `reposhelf` unter dem Account
`Fenron-dev`. Go-Modulpfad: `github.com/Fenron-dev/reposhelf`.

**Begründung:** Vorschlag aus F-002; passt zum Programmnamen `reposhelf` und
zum bestehenden GitHub-Account.

**Folgen:** Alle Importpfade beginnen mit `github.com/Fenron-dev/reposhelf/`.

### D-014 · Lizenz GPL-3.0-or-later
2026-11-08 · gültig (beantwortet F-003)

**Entscheidung:** RepoShelf steht unter der **GNU General Public License,
Version 3 oder neuer** (SPDX: `GPL-3.0-or-later`). Volltext in `LICENSE`.

**Begründung:** Wunsch von Dennis. Copyleft hält Ableger offen; „or later“ ist
bei GNU-Projekten und Forgejo üblich und macht Forgejo-Code direkt übernehmbar.

**Folgen:**
- Gitea-Code (MIT) darf weiter übernommen werden; der MIT-Hinweis bleibt im
  Kopfkommentar erhalten (MIT ist GPL-kompatibel).
- Forgejo-Code wird übernehmbar → D-015.
- Abhängigkeiten müssen GPL-3.0-kompatibel sein (z. B. MIT, BSD, Apache-2.0,
  MPL-2.0; **nicht** GPL-2.0-only).
- Neue Go-Dateien tragen die Kopfzeile
  `// SPDX-License-Identifier: GPL-3.0-or-later`.

### D-015 · Gitea und Forgejo als Code-Quellen
2026-11-08 · gültig (ersetzt D-002; beantwortet F-012)

**Entscheidung:** RepoShelf hängt weiterhin nicht von Gitea/Forgejo als
laufendem Programm ab. Code aus **Gitea (MIT)** und aus **Forgejo (auch
≥ v9, GPL-3.0-or-later)** darf übernommen werden.

**Begründung:** Mit D-014 ist RepoShelf selbst GPL-3.0-or-later; der Grund für
das Forgejo-Verbot in D-002 („würde RepoShelf GPL-pflichtig machen“) entfällt.

**Folgen:** Für jede Übernahme Herkunftskommentar (Repo, Pfad, Commit,
Lizenz) und Eintrag in `THIRD_PARTY.md` (T-0.6). `AGENTS.md` und
`KONZEPT.md` 7.2 entsprechend angepasst.

### D-016 · Standard-Port 7373
2026-11-08 · gültig (beantwortet F-008)

**Entscheidung:** Die Weboberfläche nutzt standardmäßig Port **7373** und hört
ohne weitere Einstellung nur auf `127.0.0.1` (Standardadresse
`127.0.0.1:7373`). Docker setzt die Adresse per Umgebungsvariable auf alle
Schnittstellen.

**Begründung:** Der ursprüngliche Vorschlag 7878 ist der Standard-Port von
Radarr, das auf Unraid oft läuft. 7373 ist nach bisherigem Wissen bei
gängigen Heimserver-Apps frei. Nur `127.0.0.1` als Vorgabe, damit eine
Desktop-Instanz nicht ungewollt im Netz erreichbar ist.

**Folgen:** Konstante in `internal/config`; Docker-Image (T-5.1) setzt
`REPOSHELF_LISTEN=:7373`.


### D-017 · Weboberfläche mit Go-Templates und htmx
2026-11-08 · gültig (beantwortet F-005)

**Entscheidung:** Die Weboberfläche besteht aus Go-Templates (`html/template`)
und htmx. htmx und das Stylesheet werden in die Binärdatei eingebettet
(`embed`); es gibt keinen Node-Build.

**Begründung:** Vorschlag aus Konzept 4.2; eine Binärdatei, nichts zu bauen
außer Go, wenig eigenes JavaScript.

**Folgen:** htmx liegt als Datei im Repo (Lizenz in `THIRD_PARTY.md`).

### D-018 · Ordnername eines Hosts ohne Port
2026-11-08 · gültig (beantwortet F-013)

**Entscheidung:** `source.host` darf einen Port enthalten
(`gitea.local:3000`). Der Ordnername unter `repos/`, `meta/`, `releases/`
usw. ist nur der Hostname **ohne** Port (`gitea.local`). Gibt es bereits ein
Repo mit demselben Hostnamen, Owner und Namen (z. B. unter einem anderen
Port), wird das Hinzufügen mit einer Meldung abgelehnt.

**Begründung:** Wunsch von Dennis: Der Port gehört nicht in den Ordnernamen
(`:` ist unter Windows ohnehin verboten). Zwei gleichnamige Repos auf
demselben Host unter verschiedenen Ports sind in der Praxis selten; der Fall
wird erkannt statt still überschrieben (Invariante 5).

**Folgen:** `repometa` prüft `source.host` als `hostname[:port]`; IPv6-Adressen
als Host werden nicht unterstützt. Pfade entstehen über `repometa.Source.Dir`.

### D-019 · Desktop-Hülle mit Wails v2
2026-11-08 · gültig (beantwortet F-004)

**Entscheidung:** Die Desktop-App nutzt **Wails v2** (stabil).

**Begründung:** Wails zeigt nur die Weboberfläche in einem nativen Fenster;
dafür reicht die stabile Version. v3 ist noch Beta.

**Folgen:** Die Desktop-Hülle ist ein eigenes Programm (`cmd/reposhelf-desktop`)
mit eigenem Build-Tag; der Kern hängt nicht von Wails ab. Gebaut wird sie nur
in CI (macOS-Runner manuell oder bei `v*`-Tag, D-012).

### D-020 · Portables Git für Windows mitliefern
2026-11-08 · gültig (beantwortet F-010)

**Entscheidung:** Das Windows-Paket enthält ein portables Git (Git for Windows,
„MinGit“) neben der Programmdatei. RepoShelf nutzt zuerst ein mitgeliefertes
Git, sonst das `git` im `PATH`; fehlt beides, gibt es eine klare Meldung.

**Begründung:** Wunsch von Dennis; RepoShelf soll unter Windows ohne
Installation laufen (portabel, USB-Stick).

**Folgen:** Der Release-Workflow lädt MinGit (GPL-2.0, als eigenständiges
Programm mitgeliefert, nicht gelinkt) mit geprüfter Prüfsumme und legt es in
`git/` neben `reposhelf.exe`. Lizenzhinweis in `THIRD_PARTY.md`.

### D-021 · Code-Volltextsuche mit git grep
2026-11-08 · gültig (beantwortet F-007)

**Entscheidung:** Die Code-Suche über alle Repos (T-9.5) läuft mit `git grep`
direkt auf den Bare-Spiegeln (Standard-Branch), ohne zusätzlichen Index.

**Begründung:** Wunsch von Dennis; keine weitere Abhängigkeit, kein
zusätzlicher Speicher, für Hunderte Repos schnell genug. Ein Index (zoekt)
bleibt möglich, falls es zu langsam wird.

**Folgen:** Suche mit Zeit- und Treffergrenzen; erreichbar in der Oberfläche,
per CLI (`reposhelf grep`) und MCP (`search_code`).

### D-022 · Instanzschlüssel als Datei im Datenordner
2026-11-08 · gültig (beantwortet F-014)

**Entscheidung:** Der Ed25519-Instanzschlüssel für den Abgleich zwischen
Instanzen liegt in `.reposhelf/instance.key` (Rechte 0600).

**Begründung:** Wunsch von Dennis. Er ist ein selbst erzeugter
Maschinenschlüssel wie ein SSH-Hostkey, verlässt die Instanz nie und macht den
portablen Betrieb (USB-Stick, Docker-Volume) möglich. Ausnahme zu Konzept 4.2
(„keine Geheimnisse im Datenordner“), das sich auf Zugangsdaten für fremde
Dienste bezieht.

**Folgen:** Wer den Datenordner kopiert, kopiert die Identität der Instanz mit;
bei Verlust einfach neu koppeln.

### D-023 · macOS-App ohne Apple-Notarisierung
2026-11-08 · gültig

**Entscheidung:** Die macOS-App wird ad-hoc signiert, aber nicht von Apple
notarisiert. Erster Start per Rechtsklick → „Öffnen“.

**Begründung:** Wunsch von Dennis; für den eigenen Gebrauch genügt das, eine
Notarisierung bräuchte ein kostenpflichtiges Apple-Entwicklerkonto.

**Folgen:** Hinweis in `docs/BETRIEB.md`. Wird die App später weitergegeben,
kann die Notarisierung im Release-Workflow ergänzt werden (Zertifikat und
Zugangsdaten als GitHub-Secrets).

### D-024 · GitHub-Zugang am Desktop: erst Token, dann Anmelden mit GitHub
2026-11-10 · gültig (F-015)

**Entscheidung:** Zuerst kann ein Token in der Oberfläche eingegeben werden
(T-9.10); er wird geprüft und im Schlüsselbund des Systems abgelegt (macOS,
Windows-Anmeldeinformationsverwaltung, Linux Secret Service). Danach kommt
„Mit GitHub anmelden“ (Device-Flow), sobald Dennis eine OAuth-App angelegt hat.

**Begründung:** Der Token-Weg funktioniert sofort ohne Vorarbeit; das Anmelden
ist bequemer, braucht aber eine registrierte OAuth-App (nur Client-ID, kein
Geheimnis).

**Folgen:** Tokens weiterhin nie im Datenordner (Konzept 4.2). Umgebungsvariablen
haben weiter Vorrang (Docker).

### D-025 · Laufzeit-Daten: Nachfrage ab 1 GB je Eintrag
2026-11-10 · gültig (F-016)

**Entscheidung:** Modelle, Pakete und Laufzeit-Images werden ohne Nachfrage
geladen, solange ein Eintrag kleiner als 1 GB ist; darüber fragt RepoShelf.
Zusätzlich gibt es eine globale Obergrenze wie bei Release-Dateien.

**Begründung:** Kleine Modelle sollen einfach mitlaufen; große Downloads kosten
spürbar Platz (Speicher ist knapp).

**Folgen:** Grenze einstellbar (global → Sammlung → Repo) wie die übrigen
Aufbewahrungsregeln.

### D-026 · Python-Pakete: Plattformen je Repo wählbar
2026-11-10 · gültig (F-017)

**Entscheidung:** Pakete wie PyTorch werden für die je Repo gewählten
Plattformen gesichert. Standard: die eigene Plattform plus linux/amd64 (Unraid).
CUDA-Varianten nur, wenn sie je Repo eingeschaltet sind.

**Begründung:** Pakete für alle Systeme kosten viel Platz und werden kaum
genutzt; Linux deckt den Betrieb im Container ab.

**Folgen:** Genaue Versionsliste (Lockfile) wird immer gesichert, auch wenn für
eine Plattform keine Pakete vorliegen.

### D-027 · Modelle werden wie Repos aktualisiert
2026-11-10 · gültig (F-018)

**Entscheidung:** Eine festgehaltene Modell-Revision bleibt erhalten; neue
Revisionen kommen beim Abgleich zusätzlich dazu, ältere schlägt die
Lösch-Prüfung vor (Papierkorb, Bestätigung).

**Begründung:** Gleiche Regeln wie für Repos und Releases; kein Verlust einer
Revision, mit der ein Tool nachweislich lief.

**Folgen:** Aufbewahrungsregeln (global → Sammlung → Repo) gelten auch für
Modelle.
