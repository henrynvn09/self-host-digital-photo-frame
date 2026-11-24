# photo-api

Lightweight, high-performance pure-Go media indexing and serving service built for Synology NAS Container Manager. Powers `MMM-MediaBackground` (and exposes the Immich-compatible REST subset) so you can drop Immich completely while keeping native **2K (2560×1440)** photo `Preview`s, silent **30 fps 1080p-max** video `Preview`s streamed over MJPEG within a bounded `Playback Window`, compact server-blurred `Backdrop`s, EXIF capture dates, worldwide offline reverse geocoding (`city`, `state`, `country`), folder-derived `Album` names, and HEIC/MP4/MOV/M4V/WebM support.

## Features
- **Read-Only Library (`:ro`)**: Never modifies or deletes original `Asset`s.
- **Synology Hygiene**: Runs as non-root `PUID:PGID` (`1026:100`) and automatically skips `@eaDir`, `#recycle`, hidden `.*` directories, and custom `EXCLUDE_PATTERNS`.
- **Incremental `Scan`**: Diffs photo and video `Asset`s by `(relative_path, mtime, size)` on startup, periodic interval (`SCAN_INTERVAL`), and manual `POST /api/admin/scan`, probing `.mp4`/`.mov`/`.m4v`/`.webm` via `ffprobe` for duration and auto-rotated display dimensions.
- **Offline Worldwide Geocoding**: Bundles GeoNames `cities1000` (~150k+ cities) into the Docker image and unloads the index between scans to keep idle RAM ~12–20MB.
- **Lazy 2K Photo & Silent 30 fps Video `Preview`s + 1GB In-Memory LRU Cache**: Scales JPEG/PNG/WebP/HEIC photos to 2560×2560 JPEG and transcodes bounded video `Playback Window`s into silent, auto-rotated, unpadded `1080p`-max `min(30, source_fps)` H.264 clips in the 1 GB in-memory LRU (`CACHE_MODE=memory`, **0 disk writes**), pacing `multipart/x-mixed-replace` MJPEG frames (trimming longer clips and looping shorter clips server-side).

## Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `PORT` | `2283` | HTTP listen port |
| `PHOTOS_DIR` | `/photos` | Read-only mounted `Library` directory |
| `DATA_DIR` | `/data` | Writable directory for `index.db` (and disk cache when `CACHE_MODE=disk`) |
| `GEONAMES_DIR` | `/app/geonames` | Bundled offline GeoNames dataset directory |
| `API_KEY` | *(empty)* | Optional static `x-api-key` required on all non-ping routes |
| `SCAN_INTERVAL` | `30m` | Periodic incremental scan interval |
| `EXCLUDE_PATTERNS` | *(empty)* | Comma-separated folder/file globs to skip (e.g. `Screenshots,Private`) |
| `MAX_WIDTH` | `2560` | Max preview width (2K native) |
| `MAX_HEIGHT` | `2560` | Max preview height (2K native, portrait & landscape) |
| `JPEG_QUALITY` | `88` | JPEG quality (1–100) |
| `CACHE_MODE` | `memory` | Cache backend: `memory` (default, in-RAM LRU, 0 disk writes), `disk`, or `hybrid` |
| `CACHE_MAX_MB` | `1024` | Max preview cache size in MB (1GB default) |
| `MAX_CONCURRENT_DECODES` | `2` | Max parallel image decodes to bound peak memory |
| `VIDEO_MAX_WIDTH` | `1920` | Max unpadded landscape video `Preview` width |
| `VIDEO_MAX_HEIGHT` | `1080` | Max unpadded landscape video `Preview` height (`1080×1920` portrait) |
| `VIDEO_FPS` | `30` | Maximum video frame rate cap (`min(30, VIDEO_FPS, source_fps)`) |
| `MAX_CONCURRENT_VIDEO_TRANSCODES` | `1` | Max concurrent `ffmpeg` video transcodes (`-threads 2`) |
| `PREBAKE_BLUR` | `false` | When `true`, pre-composites blurred background bars on the NAS |
| `FRAME_WIDTH` | `1440` | Target frame width when `PREBAKE_BLUR=true` |
| `FRAME_HEIGHT` | `2560` | Target frame height when `PREBAKE_BLUR=true` |

## Endpoints

- `GET /api/server/ping` — Healthcheck (`{"res":"pong"}`, unauthenticated)
- `GET /api/server/version` — Immich version handshake
- `POST /api/search/metadata` — Date- and `type`-filtered metadata search (`""` / `"ALL"` / `"IMAGE"` / `"VIDEO"`)
- `POST /api/search/smart` — Aliased to `/api/search/metadata` (ignores ML query text)
- `POST /api/search/random` — Unified or `type`-filtered `Anniversary Permutation` (`Cyclic Slice`) and random sample
- `GET /api/memories?for=<ISO-date>` — "On This Day" photo and video buckets across prior years
- `GET /api/assets/{id}` — Single `Asset` metadata (`type`, `durationSeconds`, `exifInfo`, `albumName`)
- `GET /api/assets/{id}/thumbnail` — Cached 2K EXIF-oriented JPEG `Preview` (poster frame for `VIDEO` `Asset`s)
- `GET /api/assets/{id}/backdrop` — Cached compact (`360×640`) pre-blurred, ~35%-darkened JPEG `Backdrop`
- `GET /api/assets/{id}/stream?window=<sec>&fps=<fps>` — Server-paced `multipart/x-mixed-replace` MJPEG video stream across the `Playback Window`
- `POST /api/admin/scan` — Trigger an immediate incremental `Scan`

## Running Tests via Docker

```bash
docker run --rm -v "$(pwd):/src" -w /src golang:1.24-bookworm sh -c "go mod tidy && go test -v ./..."
```
