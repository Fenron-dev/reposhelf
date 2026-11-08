# Selbst gehosteter GitHub-Actions-Runner auf Unraid (T-0.10)

Geprüft am 2026-11-08. Ergebnis: **machbar und empfehlenswert** für private Repos.

- GitHub erlaubt selbst gehostete Runner für private Repos ohne Kosten; die
  Laufzeit zählt **nicht** gegen die 2.000 Freiminuten (D-012).
- Auf Unraid als Docker-Container, z. B. mit dem offiziellen Runner-Image
  `ghcr.io/actions/actions-runner` oder einem Community-Template
  („github-runner“). Benötigt: Registrierungs-Token (Repo- oder
  Organisationsebene), Label z. B. `unraid`, optional Docker-Socket für
  Container-Jobs.
- Sicherheit: Selbst gehostete Runner nur für **eigene private** Repos verwenden –
  bei öffentlichen Repos könnten Pull Requests fremden Code auf dem Server
  ausführen. Runner als eigener Benutzer, ohne Zugriff auf `/mnt/user` außer
  einem Arbeitsordner.
- Einbindung: In Workflows `runs-on: [self-hosted, unraid]`. RepoShelfs eigene
  CI (`.github/workflows/ci.yml`) bleibt bei `ubuntu-latest`, kann aber auf den
  Runner umgestellt werden, sobald er läuft.

Abgrenzung zu Phase 7: RepoShelf hat eine **eigene** Runner-Rolle (Builds in
Containern auf einer RepoShelf-Instanz, `act` für Workflows) und kann
GitHub Actions als externen Runner anstoßen. Ein GitHub-Runner auf Unraid ist
davon unabhängig und ergänzt beides.
