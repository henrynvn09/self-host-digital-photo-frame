# AGENTS.md - Photoframe

## Project Overview

Unified repository for a 27-inch 2K family digital photo frame organized into two top-level folders (`server/` and `client/`):
- **`server/`**: Complete Synology NAS Container Manager stack (`server/docker-compose.yml` orchestrating both services on a private internal Docker network):
  - **`server/photo-api/`**: Lightweight, high-performance Go + SQLite + GeoNames service that indexes the NAS `Library` directly (read-only) and exposes the media search, `Preview`, and `Backdrop` REST API used by `MMM-MediaBackground` (preserving Immich-compatible endpoints).
  - **`server/magicmirror/`**: Thin Docker image extending pinned `karsten13/magicmirror` with commit-pinned 3rd-party modules (`MMM-Worldclock`, `MMM-OpenWeatherMapForecast`), bundled first-party `MMM-MediaBackground` + `MMM-GoogleSheetsToDo` + built-in `calendar`, secret-free sample config, and custom CSS.
- **`client/`**: Raspberry Pi 3B display client (X11 mode, unified systemd display schedule, event-driven GPIO PIR motion sensor, journald logging).

Each folder (`server/`, `server/photo-api/`, `server/magicmirror/`, `client/`) owns its own `README.md` setup guide.

## Agent skills

### Issue tracker

Issues and specs are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the five default canonical triage labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context layout (`CONTEXT.md` + `docs/adr/` at the repo root). See `docs/agents/domain.md`.
