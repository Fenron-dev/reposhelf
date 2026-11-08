# RepoShelf – UI-Entwurf

**Online ansehen und bearbeiten:** https://claude.ai/artifact/THFARjZJtNP2MgeEigJcq8
(Canvas „RepoShelf – UI-Entwurf“; im Play-Modus sind die Ansichten verlinkt.)

Die Dateien unter `quellen/` sind die Quellen dieses Canvas (`.dc.html`).
Sie brauchen die Laufzeit des Canvas (`support.js`) und sind **nicht** direkt
im Browser lauffähig – sie dienen als exakte Vorlage für Struktur, Texte und
Werte. Wird der Entwurf online geändert, hier die Quellen aktualisieren
(Status-Eintrag nicht vergessen).

## Ansichten

| Datei | Ansicht | Umsetzung |
|---|---|---|
| `quellen/Main.dc.html` | Bibliothek: Suche, Filter-Chips, Repo-Tabelle | T-1.9 |
| `quellen/RepoDetail.dc.html` | Repo-Detail: README, Schlagwörter, Beziehungen, Backlinks, Aufbewahrung, Build-Kapsel | T-1.10, T-2.3 |
| `quellen/AddRepo.dc.html` | Repo hinzufügen (Dialog) | T-1.11 |
| `quellen/Cleanup.dc.html` | Lösch-Prüfung beim Start | T-3.3 |
| `quellen/Builds.dc.html` | Builds & Runner, KI-Zugriff (MCP) | T-7.x |
| `quellen/Sidebar.dc.html` | Gemeinsame Seitenleiste | T-1.9 |

Alle Zahlen, Versionen und die Repos `beispiel/…` sowie `fero-plugin-royalroad`
sind **Beispieldaten**.

## Design-Tokens (dunkles Theme, D-007)

Als CSS-Variablen anlegen, nicht fest ins Markup schreiben (Themes folgen in T-9.3).

| Token | Wert | Verwendung |
|---|---|---|
| `--bg` | `#0E1014` | Seitenhintergrund |
| `--bg-overlay` | `#07080B` | Hintergrund hinter Dialogen |
| `--surface` | `#12151B` | Karten, Tabellen, Seitenleiste |
| `--surface-2` | `#151821` | Eingabefelder, Chips |
| `--surface-3` | `#1B1F28` | Schlagwort-Chips |
| `--border` | `#232833` | Trennlinien |
| `--border-strong` | `#2A303C` | Rahmen von Feldern/Buttons |
| `--text` | `#E7E9EE` | Haupttext |
| `--text-2` | `#C9CED8` | Sekundärtext |
| `--text-muted` | `#9BA3AF` | Hinweise, Labels (Kontrast ≥ 4,5:1 auf `--surface`) |
| `--accent` | `#F2B544` | Bernstein: Primäraktion, aktive Navigation |
| `--accent-hover` | `#FFD27A` | Links beim Überfahren |
| `--accent-bg` | `#262115` | Hintergrund aktiver Elemente / Art „Eigen“ |
| `--on-accent` | `#1A1206` | Text auf Akzentfläche |
| `--ok` | `#86EFAC` | Aktuell / erfolgreich |
| `--running` | `#FCD34D` | Läuft |
| `--warn` | `#FDBA74` | Upstream weg, Speicherwarnung |
| `--error` | `#FCA5A5` | Fehlgeschlagen |
| `--kind-mirror` | `#7DD3FC` auf `#1A2430` | Art „Spiegel“ |
| `--kind-fork` | `#C4B5FD` auf `#241C30` | Art „Fork“ |

Status nie nur über Farbe: immer Symbol + Text (●, ◐, ▲, ✓, ✕).

**Schrift:** Inter (Text), Space Grotesk (Überschriften), JetBrains Mono
(Pfade, Versionen, Größen, Schlagwörter). Für die portable App lokal einbetten,
nicht von Google Fonts laden.

**Maße:** Radius 6 (Chips) · 8–10 (Buttons, Felder) · 14 (Karten) · 18 (Dialoge).
Klickflächen ≥ 44 px Höhe (36 px für Chips/Sekundär). Abstände im 4er-Raster.
Seitenleiste 240–248 px, klappt bei schmalen Fenstern über den Inhalt um.
