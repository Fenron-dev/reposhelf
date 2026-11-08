# RepoShelf – Hinweise für Claude Code

Die verbindlichen Regeln für alle Agenten stehen in `AGENTS.md` und werden hier
eingebunden:

@AGENTS.md

## Ergänzungen für Claude Code

- Beginne jede Sitzung mit `docs/STATUS.md` (Aktueller Stand + Übergabe-Notiz).
  Sag kurz, welche Aufgabe du übernimmst, bevor du anfängst.
- Nutze die Aufgabenliste von Claude Code für die Schritte **innerhalb** einer
  Aufgabe. Der projektweite Stand gehört aber immer in `docs/STATUS.md` – die
  Aufgabenliste geht mit der Sitzung verloren, `STATUS.md` nicht.
- Vor dem Ende einer Sitzung (auch bei Abbruch oder halb fertiger Arbeit):
  Übergabe-Notiz und Verlauf in `docs/STATUS.md` aktualisieren, damit Codex
  oder die nächste Sitzung nahtlos weitermachen kann.
- Keine lokalen Docker-, Wails- oder `act`-Builds (Speicher). Siehe
  „Bauen und Ausführen“ in `AGENTS.md`.
