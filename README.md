# self-host-digital-photo-frame

A lightweight, self-hosted **27-inch 2K digital photo frame** powered by a Synology NAS and a Raspberry Pi 3B—replacing heavy Immich stacks (~2–4GB RAM) with a ~20MB pure-Go photo API + MagicMirror² + motion-aware Pi client.

## Components & Setup Guides

Each component owns its own setup guide:

- **[`photo-api/README.md`](./photo-api/README.md)** — Pure-Go + SQLite + offline worldwide GeoNames photo API (read-only NAS library, 2K JPEG previews, HEIC support, 1GB LRU cache, Immich-compatible REST subset).
- **[`magicmirror/README.md`](./magicmirror/README.md)** — Thin Dockerized MagicMirror² server with pinned modules (`MMM-ImmichSlideShow`, `MMM-Worldclock`, `MMM-OpenWeatherMapForecast`).
- **[`client/README.md`](./client/README.md)** — Raspberry Pi 3B X11 kiosk setup, unified systemd `Display Schedule`, and event-driven GPIO PIR motion sensor.
- **[`PRD.md`](./PRD.md)** — Full architecture diagram, performance optimizations, and design rationale.

## Quick Start (Synology Container Manager)

1. Copy `.env.example` to `.env` and set your DSM `PUID`/`PGID` (`id <username>`), `PHOTOS_PATH`, `DATA_PATH`, and `API_KEY`.
2. Copy `magicmirror/config/config.js.example` to `magicmirror/config/config.js` and add your `API_KEY` + OpenWeatherMap key.
3. Deploy both containers together on a private internal Docker network:
   ```bash
   docker compose up -d --build
   ```
4. Point your Raspberry Pi 3B client (see [client/README.md](./client/README.md)) to `http://<your-nas-ip>:8036`.
