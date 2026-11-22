# photo-api

Lightweight, high-performance pure-Go photo indexing and serving service built for Synology NAS Container Manager. Exposes the Immich REST subset expected by `MMM-ImmichSlideShow` so you can drop Immich completely while keeping native **2K (2560×1440)** quality, EXIF capture dates, worldwide offline reverse geocoding (`city`, `state`, `country`), folder-derived `Album` names, and HEIC support.

## Features
- **Read-Only Library (`:ro`)**: Never modifies or deletes original photos.
- **Synology Hygiene**: Runs as non-root `PUID:PGID` (`1026:100`) and automatically skips `@eaDir`, `#recycle`, hidden `.*` directories, and custom `EXCLUDE_PATTERNS`.
- **Incremental `Scan`**: Diffs by `(relative_path, mtime, size)` on startup, periodic interval (`SCAN_INTERVAL`), and manual `POST /api/admin/scan`.
- **Offline Worldwide Geocoding**: Bundles GeoNames `cities1000` (~150k+ cities) into the Docker image and unloads the index between scans to keep idle RAM ~12–20MB.
- **Lazy 2K `Preview`s + 1GB LRU Cache**: Scales JPEG/PNG/WebP/HEIC on demand to fit within 2560×1440 JPEG (quality 88) with EXIF auto-rotation and singleflight deduplication.

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `2283` | HTTP listen port |
| `PHOTOS_DIR` | `/photos` | Read-only mounted `Library` directory |
| `DATA_DIR` | `/data` | Writable directory for `index.db` and `cache/` |
| `GEONAMES_DIR` | `/app/geonames` | Bundled offline GeoNames dataset directory |
| `API_KEY` | *(empty)* | Optional static `x-api-key` required on all non-ping routes |
| `SCAN_INTERVAL` | `30m` | Periodic incremental scan interval |
| `EXCLUDE_PATTERNS` | *(empty)* | Comma-separated folder/file globs to skip (e.g. `Screenshots,Private`) |
| `MAX_WIDTH` | `2560` | Max preview width (2K native) |
| `MAX_HEIGHT` | `1440` | Max preview height (2K native) |
| `JPEG_QUALITY` | `88` | JPEG quality (1–100) |
| `CACHE_MAX_MB` | `1024` | Max preview disk cache size in MB (LRU eviction) |

## Endpoints

- `GET /api/server/ping` — Healthcheck (`{"res":"pong"}`, unauthenticated)
- `GET /api/server/version` — Immich version handshake
- `POST /api/search/metadata` — Date-filtered metadata search (powers `anniversary` & year-slice `search`)
- `POST /api/search/smart` — Aliased to `/api/search/metadata` (ignores ML query text)
- `POST /api/search/random` — Randomized asset sample with optional date bounds
- `GET /api/memories?for=<ISO-date>` — "On This Day" buckets across prior years
- `GET /api/assets/{id}` — Single asset metadata
- `GET /api/assets/{id}/thumbnail` — Cached 2K EXIF-oriented JPEG `Preview`
- `POST /api/admin/scan` — Trigger an immediate incremental `Scan`

## Running Tests via Docker

```bash
docker run --rm -v "$(pwd):/src" -w /src golang:1.24-bookworm sh -c "go mod tidy && go test -v ./..."
```
