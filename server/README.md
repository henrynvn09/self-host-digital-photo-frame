# server (Synology NAS Stack)

Single Synology Container Manager stack (`docker-compose.yml`) running two containers on a private internal Docker network (`photoframe_net`):

- **[`photo-api/`](./photo-api/README.md)** — Pure-Go + SQLite + offline worldwide GeoNames photo API (read-only NAS `Library`, lazy 2K JPEG `Preview`s, HEIC support, 1GB LRU cache, Immich-compatible REST subset; internal-only port `2283`).
- **[`magicmirror/`](./magicmirror/README.md)** — Thin MagicMirror² image with commit-pinned modules (`MMM-ImmichSlideShow`, `MMM-Worldclock`, `MMM-OpenWeatherMapForecast`; exposed on host port `8036` for the Raspberry Pi `Frame Client`).

## Synology Container Manager Setup

1. **Find your DSM `PUID` and `PGID`** via SSH on your NAS:
   ```bash
   id <your-dsm-username>
   # Example: uid=1026(henry) gid=100(users)
   ```
2. **Configure environment variables**:
   ```bash
   cd server
   cp .env.example .env
   nano .env
   ```
   Set `PUID`, `PGID`, `PHOTOS_PATH` (mounted read-only `:ro`), `DATA_PATH` (writable folder owned by your DSM user), and `API_KEY`.
3. **Configure MagicMirror** (secret-free template):
   ```bash
   cp magicmirror/config/config.js.example magicmirror/config/config.js
   nano magicmirror/config/config.js
   ```
   Set `apiKey` to match `API_KEY` in `.env` and add your OpenWeatherMap API key.
4. **Deploy both containers**:
   ```bash
   docker compose up -d --build
   ```
5. **Point your Raspberry Pi `Frame Client`** (see [`../client/README.md`](../client/README.md)) to `http://<your-nas-ip>:8036`.
