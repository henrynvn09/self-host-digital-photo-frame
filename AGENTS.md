# AGENTS.md - Photoframe

## Project Overview

Unified repository for a 27-inch 2K family digital photo frame combining:
- **`photo-api/`**: Lightweight, high-performance Go + SQLite + GeoNames service running on a Synology NAS that indexes NAS photo folders directly (read-only) and exposes the Immich-compatible REST subset used by `MMM-ImmichSlideShow`.
- **`magicmirror/`**: Thin Docker image extending `karsten13/magicmirror` with pinned 3rd-party modules (`MMM-ImmichSlideShow`, `MMM-Worldclock`, `MMM-OpenWeatherMapForecast`), secret-free sample config, and custom CSS.
- **`client/`**: Raspberry Pi 3B display client (X11 mode, unified systemd display schedule, event-driven GPIO PIR motion sensor, journald logging).
- **Root `docker-compose.yml`**: Single Synology Container Manager Project orchestrating both `magicmirror` and `photo-api` on a private internal Docker network.

Each top-level folder (`client/`, `magicmirror/`, `photo-api/`) owns its own `README.md` setup guide.

## Agent skills

### Issue tracker

Issues and specs are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the five default canonical triage labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout (`CONTEXT.md` + `docs/adr/` at the repo root). See `docs/agents/domain.md`.
