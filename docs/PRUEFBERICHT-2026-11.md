# RepoShelf – Prüfbericht November 2026

Stand: `v0.5.2` (Commit `3e7c143`). Geprüft von Claude Code am 2026-11-11 im Auftrag
von Dennis: Sicherheitslücken, Probleme, Optimierungen, fehlende Funktionen.
Es wurde **kein Code geändert**; die Befunde stehen als `R-011` bis `R-027` in
`docs/STATUS.md`, die Funktionsvorschläge als offene Frage `F-019`.

Geprüft wurde durch Lesen des Codes (Anmeldung, Web, Git-Aufrufe, Importe, Runner, MCP,
Instanz-Abgleich) und mit `govulncheck`. Nicht geprüft: Angriffe gegen eine laufende
Instanz, die Wails-Hülle im Detail, das Verhalten auf FAT/exFAT-Datenträgern.

## 1. Was gut gelöst ist

- Tokens nur als SHA-256-Hash gespeichert, Vergleich in konstanter Zeit, Datei `0600`.
- README/Markdown: kein rohes HTML, gefährliche Links werden gefiltert; Roh-Dateien mit
  eigener, strenger CSP (`sandbox`).
- Formulare der Oberfläche prüfen die Herkunft (Origin/Referer).
- Pfade aus Archiven und aus dem Netz werden geprüft (ZIP, tar, Releases, LFS-IDs);
  Symlinks aus fremden Repos bleiben beim Auspacken im Projektordner.
- Git-Zugangsdaten gehen nur an den passenden Host (`http.<url>.extraHeader`), nie in
  URLs oder Logs. Host-Tokens liegen im Schlüsselbund.
- MCP: kein Löschen, Rechte je Token, eingebauter Schutz der Bibliothek gegen
  DNS-Rebinding; fremde Inhalte werden als „nicht vertrauenswürdig“ markiert.
- Weitergeleitete Anfragen (Proxy-Header) gelten nicht als „lokal“.

## 2. Sicherheitsbefunde

Schwere: `hoch` · `mittel` · `gering` · `Hinweis`. Reihenfolge = empfohlene Reihenfolge.

### R-011 · hoch · Fremde Webseiten können als „lokaler Nutzer“ handeln

`internal/server/server.go` (`isLocal`, `resolve`), `internal/web/web.go` (`sameOrigin`)

Anfragen von `127.0.0.1` gelten ohne Anmeldung als Verwalter. Geprüft wird nur die
Absenderadresse, nicht der `Host`-Kopf. Mit „DNS-Rebinding“ lässt eine präparierte
Webseite ihren eigenen Namen auf `127.0.0.1` zeigen; der Browser hält sie dann für
dieselbe Herkunft wie RepoShelf, und auch die Herkunftsprüfung der Formulare passt
(Origin = Host). Folge: Die Seite kann alles, was der lokale Nutzer kann – alle Repos
lesen (auch private), Tokens anlegen, Einstellungen ändern, lokale Ordner „übernehmen“
(und damit lesen oder in den Papierkorb schieben) und Builds starten.
Betroffen: Desktop-App und jede Instanz, die man am selben Rechner im Browser nutzt.
Chrome erschwert das inzwischen, Firefox und Safari nicht zuverlässig.

**Abhilfe:** „Lokal“ nur, wenn der `Host`-Kopf `localhost`, `127.0.0.1` oder `[::1]`
ist (plus die eingestellte öffentliche Adresse); alle anderen Hosts auf dem
Loopback-Anschluss ablehnen.

### R-013 · hoch · Das Repo bestimmt selbst, welche Geheimnisse sein Build bekommt

`internal/runner/exec.go:83`

`reposhelf-build.yaml` nennt unter `secrets:` Namen; RepoShelf reicht dann
`REPOSHELF_SECRET_<NAME>` in den Container. Die Datei stammt aber aus dem (fremden)
Repo. Wer ein Repo baut, das `secrets: [NPM_TOKEN]` einträgt, übergibt das Geheimnis an
fremden Code mit Netzzugang. Das Konzept verlangt „nur explizit pro Build freigegeben“.

**Abhilfe:** Freigabe in RepoShelf (je Repo in `repo.yaml` oder beim Start des Builds),
Standard: keine; für Spiegel nie ohne Bestätigung.

### R-014 · hoch · `act`-Läufe sind nicht dicht (Docker-Zugang, Workflow-Optionen)

`internal/runner/exec.go` (`runAct`)

`act` bindet standardmäßig den Docker-Anschluss des Hosts in die Job-Container ein, und
ein Workflow darf eigene Container-Optionen und Laufwerke angeben. Ein Workflow aus einem
fremden Repo kann damit den Docker des Hosts steuern – praktisch Vollzugriff auf den
Rechner. Das widerspricht D-008 („fremder Code nur in Containern“) dem Sinn nach.

**Abhilfe:** `act` mit `--container-daemon-socket -` starten; `act` für Spiegel nur nach
ausdrücklicher Bestätigung mit Warnhinweis oder nur für eigene Projekte; Workflows mit
`container.options`/`volumes`/`services` ablehnen. (Annahme zum Standardverhalten von
`act` vor der Umsetzung gegen die verwendete Version 0.2.97 bestätigen.)

### R-012 · mittel · REST-API ohne Schutz vor Anfragen fremder Seiten

`internal/api/api.go` (`POST /api/repos`, `POST /api/sync/…`)

Die Web-Formulare prüfen die Herkunft, die API nicht – und sie verlangt auch keinen
JSON-Inhaltstyp. Eine beliebige Webseite kann deshalb im Hintergrund eine Anfrage an
`http://127.0.0.1:7373/api/repos` schicken; für den lokal vertrauten Nutzer wird dann
ein Repo aufgenommen (Download, Plattenplatz) oder ein Abgleich gestartet.

**Abhilfe:** `Content-Type: application/json` verlangen und Herkunft prüfen, sobald
kein Token mitgeschickt wird.

### R-015 · mittel · Artefakte können Dateien des Hosts einsammeln

`internal/runner/exec.go` (`collect`)

Muster wie `dist/*` werden nach dem Build auf dem Host ausgewertet. Legt der Build im
Container `dist` als Verweis auf `/` an, folgt die Auswertung dem Verweis und kopiert
Dateien des Hosts (soweit RepoShelf sie lesen darf: andere private Repos, der
Instanzschlüssel, am Desktop auch persönliche Dateien) in die Artefakte. Lesen kann sie,
wer Lesezugriff auf RepoShelf hat.

**Abhilfe:** Jeden Treffer mit aufgelösten Verweisen prüfen – er muss im Arbeitsordner
liegen; Verweise nicht verfolgen.

### R-016 · mittel · Submodule werden ungefragt und rekursiv aufgenommen

`internal/core/extras.go` (`syncSubmodules`)

Was in `.gitmodules` eines gespiegelten Repos steht, wird automatisch als eigener
Spiegel aufgenommen (bis 20 je Abgleich), und für diese gilt dasselbe. Fremde Daten
lösen so Downloads aus – auch von Adressen im Heimnetz – und können die Platte füllen.

**Abhilfe:** Submodule als Vorschlag anzeigen (wie Beziehungen) oder begrenzen: nur
derselbe Host, Tiefe 1, Obergrenze je Repo, Größenprüfung vorab.

### R-017 · mittel · Go-Version mit bekannter Schwachstelle

`go.mod` (Go 1.27.3)

`govulncheck` meldet GO-2026-6859 (Absturz des HTTP/1-Servers durch präparierte
Anfragen), behoben in Go 1.27.4. Sonst keine Treffer in den Abhängigkeiten.

**Abhilfe:** Go auf 1.27.4 heben; `govulncheck` in die CI aufnehmen.

### R-018 · gering · Kein Schutz gegen Durchprobieren von Tokens

`internal/server/server.go` (`login`, `resolve`)

Fehlversuche werden weder gezählt noch verzögert. Die von RepoShelf erzeugten Tokens
sind lang genug; das Admin-Token aus `REPOSHELF_ADMIN_TOKEN` wählt aber der Nutzer.

**Abhilfe:** Verzögerung/Sperre nach Fehlversuchen je Absender; Mindestlänge für das
Admin-Token mit Warnung in „doctor“.

### R-019 · gering · Weiterleitung nach der Anmeldung auf fremde Seiten möglich

`internal/server/server.go` (`login`): `next=/\evil.example` besteht die Prüfung
(„beginnt mit `/`, nicht mit `//`“); Browser lesen `/\` wie `//`.
**Abhilfe:** auch `\` ablehnen bzw. nur Pfade ohne Schema/Host zulassen (`url.Parse`).

### R-020 · gering · Sitzungen

`internal/server/server.go`: abgelaufene Sitzungen werden nie entfernt (wächst langsam);
das Cookie ist hinter einem HTTPS-Proxy nicht als „Secure“ markiert; nach einem Neustart
sind alle abgemeldet.
**Abhilfe:** Aufräumen beim Anlegen; `Secure` auch bei `X-Forwarded-Proto: https`.

### R-021 · gering · Kopplung zweier Instanzen im ungeschützten Netz

`internal/peer/peer.go`: Der Kopplungscode hat 40 Bit; sein Nachweis (HMAC) wird
mitgeschickt und lässt sich mitlesen und durchprobieren. Wer im selben Netz Verbindungen
umleiten kann, könnte binnen der 10 Minuten einen eigenen Schlüssel koppeln. Der danach
angezeigte Fingerprint-Vergleich fängt das ab – wenn man ihn macht. Signierte Anfragen
tragen keine Einmalnummer und sind innerhalb des Zeitfensters wiederholbar.
**Abhilfe:** Kopplung mit PAKE (z. B. SPAKE2) oder längerem Code; Einmalnummern merken;
HTTPS zwischen Instanzen empfehlen.

### R-022 · gering · Werte auf der Docker-Kommandozeile

`internal/runner/exec.go`, `capsule.go`: Geheimnisse stehen als `-e NAME=wert` in der
Prozessliste; Image-Namen aus `reposhelf-build.yaml`/`Dockerfile` werden ungeprüft
übergeben (ein führendes `-` wäre eine Docker-Option).
**Abhilfe:** `-e NAME` und Wert über die Umgebung; Image-Namen prüfen, `--` setzen.

### R-023 · gering · Datenschutz: README-Bilder kommen von fremden Servern

`internal/web/web.go` (CSP `img-src … https:`): Beim Ansehen eines README lädt der
Browser eingebettete Bilder vom Original-Server – der sieht damit die eigene IP-Adresse,
und die Bilder fehlen, wenn das Original verschwindet.
**Abhilfe:** Bilder beim Abgleich mitsichern und lokal ausliefern (siehe Vorschlag V-4),
sonst nur nach Klick laden.

### R-024 · Hinweis · Git-Härtung

`internal/gitcli/gitcli.go`: keine Objektprüfung beim Holen (`fetch.fsckObjects`), keine
Einschränkung der Protokolle (`GIT_ALLOW_PROTOCOL`), die globale Git-Konfiguration des
Nutzers wirkt mit (z. B. `core.hooksPath`, `url.insteadOf`). Auf FAT/exFAT-Datenträgern
(portabler Betrieb) kann Git „dubious ownership“ melden – bitte einmal mit einem
USB-Stick prüfen; Abhilfe wäre `safe.directory` für den Datenordner über die Umgebung.

### R-025 · Hinweis · Dateinamen in Antwortköpfen

`internal/web/handlers.go` (`repoRaw`, `ServeFileSafe`): Dateinamen aus Repos stehen
ungeprüft in `Content-Disposition` (ein `"` im Namen bricht die Angabe).
**Abhilfe:** `mime.FormatMediaType` verwenden.

### R-027 · Hinweis · Weitere Härtung

Laufzeit-Image-Bau ohne Speicher-/CPU-Grenze; ZIP-Import mit enthaltenem `.git` holt die
Geschichte per `git fetch` aus dem ausgepackten (fremden) Ordner – besser über ein
Bundle oder mit gesperrten Hooks/Konfiguration.

## 3. Probleme und Optimierungen

- **R-026 · gering · Protokoll wächst unbegrenzt:** `activity.jsonl` wird nie gekürzt und
  bei jedem Abruf ganz gelesen (`internal/core/events.go`). Abhilfe: von hinten lesen,
  nach Größe/Alter rotieren.
- **Ordner-Übernahme liest große Dateien zweimal** (Kopieren mit Prüfsumme, danach
  Prüfung). Die Prüfsumme vom Kopieren merken spart bei 10-GB-Modellen Minuten.
- **Suchindex:** Bei jeder Änderung an einem Issue werden alle Issue-Dateien des Repos
  neu gelesen und das README per Git geholt. Für Repos mit Tausenden Issues spürbar;
  Abhilfe: Text je Repo zwischenspeichern.
- **Modell-Ordner für Programme:** ein Git-Aufruf je kleiner Datei; `git cat-file --batch`
  würde das bündeln.
- **Sitzungen und Seiten-Zwischenspeicher** leben nur im Speicher – nach Neustart ist der
  erste Seitenaufruf wieder langsam (Windows).
- **Kein gzip** für HTML/JSON-Antworten – im Heimnetz egal, über einen Tunnel spürbar.
- **Prüflücken:** kein Test auf echtem exFAT-Datenträger; `act`/Docker nur im Prüflauf,
  nicht in der CI; keine Last-/Fuzz-Tests für die Git- und LFS-Endpunkte.

## 4. Fehlende Funktionen – Vorschläge (F-019)

Nutzen für ein Archiv, grob nach Wirkung sortiert. Aufwand: klein (≤ 1 Tag), mittel,
groß.

| Nr. | Vorschlag | Nutzen | Aufwand |
|---|---|---|---|
| V-1 | **Integritätsprüfung nach Zeitplan:** `git fsck`, Prüfsummen aller Release-Dateien, LFS-Objekte und Kapseln; Bericht und Warnung bei Schäden | Ein Archiv, das Bitfäule nicht bemerkt, ist keins | mittel |
| V-2 | **Benachrichtigungen:** neue Version, Original verschwunden/archiviert, Force-Push, Abgleich seit Tagen fehlgeschlagen – als Desktop-Meldung, Webhook (ntfy, Discord), E-Mail | Man erfährt Wichtiges, ohne nachzusehen | mittel |
| V-3 | **Startseite „Was ist neu“:** neue Releases/Commits seit dem letzten Besuch, Speicher je Repo/Sammlung, Problemfälle | Überblick statt Liste | mittel |
| V-4 | **README-Bilder und verlinkte Dokumente mitsichern** (behebt R-023) | Vollständiges Archiv, kein Nachladen von fremden Servern | mittel |
| V-5 | **Repo wiederbeleben:** einen Spiegel mit einem Klick zu einem neuen Ziel (GitHub, Gitea) hochladen, inkl. Releases | Der eigentliche Zweck, wenn ein Original verschwindet | mittel |
| V-6 | **Commit- und Vergleichsansicht:** Verlauf blättern, Diff einer Änderung, zwei Versionen vergleichen | „Was hat sich seit v1.2 geändert?“ ohne Klonen | mittel |
| V-7 | **Feeds:** RSS/Atom für neue Versionen (alle Repos, je Sammlung) | Passt in jeden Feed-Reader | klein |
| V-8 | **Sichtbarkeit je Repo:** „privat“ bleibt bei anonymem Lesen und für Agenten-Tokens verborgen; Tokens auf Sammlungen beschränken | Feiner als „alles oder nichts“ | mittel |
| V-9 | **Container-Images sichern:** Images aus Registries (ghcr, Docker Hub) nach Tag mitsichern, wie Release-Dateien | Viele Werkzeuge gibt es nur als Image | mittel |
| V-10 | **Weitere Modell-Quellen:** Hugging-Face-Datensätze und -Spaces, Ollama-/GGUF-Modelle; „welche Modelle nutzt welches Werkzeug“ als Übersicht | Rundet T-9.11 ab | mittel |
| V-11 | **Sterne beobachten:** neue gesternte/eigene GitHub-Repos automatisch (oder als Vorschlag) aufnehmen | Nichts mehr vergessen | klein |
| V-12 | **„In RepoShelf sichern“ aus dem Browser:** Lesezeichen-Knopf oder kleine Erweiterung | Ein Klick statt Link kopieren | klein |
| V-13 | **Abhängigkeits- und Lizenzübersicht:** Stückliste (SBOM) je Version aus den Abhängigkeitsdateien, Lizenzen über alle Repos | Wissen, was man da archiviert | mittel |
| V-14 | **Speicherplanung:** Verlauf des Verbrauchs, Vorhersage, Obergrenze je Sammlung mit Warnung | Wichtig bei knappem Platz | klein |
| V-15 | **Selbst-Aktualisierung:** Hinweis auf neue RepoShelf-Version, Desktop-Update mit einem Klick | Bequemlichkeit | klein |
| V-16 | **Handy-Ansicht/PWA** und Tastaturkürzel-Übersicht | Unterwegs nachsehen | klein |
| V-17 | **Zweite Sicherung prüfen:** Anzeige je Repo, auf welchen gekoppelten Instanzen es liegt und wie alt der Stand dort ist („3-2-1“) | Vertrauen in den Abgleich | klein |
| V-18 | **Zusammenfassungen durch Agenten:** „Was kann dieses Projekt, wofür habe ich es gesichert?“ als Notizvorschlag über MCP | Ordnung in großen Sammlungen | klein |

## 5. Empfohlene Reihenfolge

1. Sofort: R-011, R-013, R-014 (hoch) – zusammen etwa ein Arbeitstag, danach Release.
2. Danach: R-012, R-015, R-016, R-017 (mittel).
3. Sammelrunde: R-018 bis R-027.
4. Funktionen nach Auswahl in F-019; mein Vorschlag für den Anfang: V-1, V-2, V-4, V-5.
