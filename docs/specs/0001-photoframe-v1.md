# Spec: Photoframe v1 — Unified Repo, Lightweight Go Photo API & Synology Stack

## Problem Statement

Running full Immich (PostgreSQL, Redis, machine-learning containers, and transcoders) on a Synology NAS just to power a 27-inch 2K family digital photo frame wastes gigabytes of RAM and adds heavy maintenance overhead. Relying on Synology Photos' unofficial internal API instead is fragile across DSM updates, complicates 2FA/session auth, and caps thumbnails at ~1280px (noticeably soft on a 2K monitor). Meanwhile, managing the Raspberry Pi 3B client, MagicMirror server, and photo backend across separate repositories with git submodules makes deployment and setup error-prone.

## Solution

Build a single unified `photoframe` repository containing three top-level folders—`photo-api/`, `magicmirror/`, and `client/` (each with its own setup `README.md`)—orchestrated on Synology Container Manager by one root `docker-compose.yml`. `photo-api` is a pure-Go + SQLite + offline GeoNames service that mounts the NAS `Library` read-only, indexes `Asset` metadata (EXIF capture date, worldwide city/state/country, folder-derived `Album`), lazily generates and LRU-caches 2K (2560×1440) JPEG `Preview`s under a 1GB disk cap, and serves the exact Immich REST subset expected by `MMM-ImmichSlideShow` so the frame works seamlessly with no custom MagicMirror module and no smart/ML search.

## User Stories

### Library Indexing & Scanning
1. As a NAS owner, I want `photo-api` to mount my `Library` read-only, so that the service can never modify, corrupt, or delete my original family photos.
2. As a Synology DSM user, I want `photo-api` to run under my configured `PUID:PGID`, so that DSM file permissions and ACLs work without running the container as root.
3. As a NAS owner, I want `photo-api` to validate `Library` readability and data directory writability on startup with clear error messages, so that permission misconfigurations fail fast and are obvious to fix.
4. As a NAS owner, I want an automatic incremental `Scan` on startup and on a configurable periodic interval, so that newly added or deleted photos appear on the frame without manual intervention.
5. As a NAS owner, I want a manual scan trigger endpoint, so that I can refresh the index immediately after uploading a batch of trip photos.
6. As a NAS owner, I want unchanged assets skipped via `(relative_path, mtime, size)` diffing, so that re-scanning tens of thousands of photos takes seconds and does not thrash NAS spinning disks.
7. As a Synology DSM user, I want the `Scan` to automatically skip `@eaDir`, `#recycle`, and hidden dot-directories, so that Synology's system thumbnails and trash are never indexed.
8. As a NAS owner, I want configurable exclusion glob patterns, so that I can hide specific folders (such as screenshots or receipts) from the photo frame.
9. As a NAS owner, I want only image assets (`.jpg`, `.jpeg`, `.png`, `.webp`, `.heic`, `.heif`) indexed while videos and RAW files are ignored, so that the frame stays fast and focused on photos.
10. As a NAS owner, I want deleted files removed from the SQLite index and their cached `Preview` evicted on the next `Scan`, so that removed photos never reappear as broken slides.

### Metadata, Dates, Location & Albums
11. As a family member viewing the frame, I want each `Asset`'s capture date extracted from EXIF `DateTimeOriginal` / `CreateDate`, so that photos appear under the day they were actually taken.
12. As a family member, I want photos missing EXIF dates (such as WhatsApp saves or old scans) to fall back to filename or folder date patterns before using file `mtime`, so that copied photos do not all cluster on the copy date in anniversary mode.
13. As a family member, I want GPS coordinates in EXIF automatically reverse-geocoded offline into city, state/province, and country across worldwide locations (including Vietnam, the US, and international travel), so that the `geo` overlay shows meaningful place names without internet access or external API keys.
14. As a family member, I want an `Asset`'s `Album` name derived from its immediate parent folder when descriptive (e.g. `2023-07 Japan Trip`) and omitted when purely numeric (e.g. `2023` or `07`), so that the `album` overlay adds context without repeating the date.
15. As a frame user, I want each `Asset` assigned a deterministic UUID derived from its relative path, so that URLs remain opaque and `MMM-ImmichSlideShow` can reliably fetch assets without hashing file contents during scan.

### Immich-Compatible Playback Modes & API
16. As a MagicMirror operator, I want `photo-api` to respond to the Immich version handshake and ping healthcheck endpoints, so that `MMM-ImmichSlideShow` connects cleanly and Docker Container Manager tracks container health.
17. As a family member, I want `anniversary` mode (±N days around today across multiple years, with per-year size limits) to work out of the box via date-range metadata queries, so that the frame surfaces seasonal memories from past years.
18. As a family member, I want `memory` mode ("On This Day" across past years) to return grouped historical memories for a given date, so that on-this-day slideshows work natively.
19. As a family member, I want `random` mode to return a truly randomized sample of assets (respecting optional `takenBefore`/`takenAfter` bounds and `size`), so that the full library gets fair rotation even with tens of thousands of photos.
20. As a MagicMirror operator, I want date-range `search` queries to work and any legacy smart-search text parameter to be safely ignored without error, so that existing year-slice configs keep working with zero ML overhead.
21. As a MagicMirror operator, I want optional `x-api-key` header (and `apiKey` query parameter) authentication enforced on all non-ping routes, so that unauthorized clients cannot browse family photos.

### 2K Preview Rendering & Bounded Caching
22. As a viewer looking at a 27-inch 2K monitor, I want `Preview`s scaled to fit within 2560×1440 (preserving aspect ratio, never upscaling smaller originals) and encoded as high-quality JPEG (default quality 88), so that photos look crisp while keeping transfers ~350–500KB for the Raspberry Pi 3B.
23. As an iPhone user, I want `.heic` and `.heif` assets decoded transparently into cached 2K JPEGs, so that modern phone photos display without manual conversion or exclusion.
24. As a viewer, I want portrait and rotated photos automatically oriented according to their EXIF orientation tag before encoding, so that no photo ever appears sideways or upside down.
25. As a NAS owner, I want `Preview`s generated lazily on first request with singleflight deduplication and cached on disk under a configurable LRU size cap (default 1GB), so that NAS CPU and disk usage remain tightly bounded.
26. As a Raspberry Pi 3B operator, I want `Preview` HTTP responses to include immutable cache headers keyed to the asset version, so that repeat views are served instantaneously without storing the library on the Pi's SD card.

### Deployment, MagicMirror & Client Setup
27. As a Synology NAS owner, I want a single root `docker-compose.yml` that runs both `magicmirror` and `photo-api` on a private internal Docker network, so that deployment is a single Container Manager Project and `photo-api` is not exposed on the LAN by default.
28. As a Synology NAS owner, I want `magicmirror` built from a thin Dockerfile with pinned versions of `MMM-ImmichSlideShow`, `MMM-Worldclock`, and `MMM-OpenWeatherMapForecast`, so that I never have to manage git submodules or run `npm install` on the NAS.
29. As a repository maintainer, I want all live API keys, OAuth tokens, and private calendar URLs excluded via `.gitignore` with clean `.example` templates, so that secrets are never accidentally committed.
30. As a Raspberry Pi 3B operator, I want the existing battle-tested `client/` scripts (X11 mode, unified systemd `Display Schedule`, event-driven PIR sensor, journald config) carried over intact with a standalone `client/README.md`, so that setting up or rebuilding the Pi is straightforward.
31. As a self-hoster, I want each top-level folder (`client/`, `magicmirror/`, `photo-api/`) to contain its own focused `README.md` setup guide linked from the root `README.md`, so that server and client setup instructions stay modular and easy to follow.

## Implementation Decisions

- **Unified Repository Structure**: Three top-level component directories (`photo-api`, `magicmirror`, `client`), each with its own `README.md`, plus a root `docker-compose.yml` and `.env.example`. The original `digital-photoframe` repo is left untouched as an archival reference.
- **Pure-Go Runtime + `heif-convert` Subprocess (ADR-0003)**: `photo-api` compiles with `CGO_ENABLED=0` using `modernc.org/sqlite` (WAL mode) and pure-Go JPEG/PNG/WebP/EXIF libraries so multi-arch Synology builds (`linux/amd64` and `linux/arm64`) are frictionless. Only `.heic`/`.heif` assets shell out to `heif-convert` (`libheif-examples`), and only once per uncached asset.
- **SQLite Schema & Indexing**: Single `assets` table storing deterministic UUID `id`, `rel_path` (unique), `mtime`, `size`, `taken_at` (RFC3339 UTC), `taken_year`, `taken_month`, `taken_day`, `city`, `state`, `country`, `album`, `orientation`, `width`, `height`, and `indexed_at`. Indexed on `taken_at`, `(taken_month, taken_day)`, and `rel_path`.
- **Offline Worldwide Reverse Geocoding**: GeoNames `cities1000` dataset (~150k+ worldwide cities + admin1/country tables) is baked into the `photo-api` Docker image at build time and indexed in memory at startup for <1ms nearest-city lookups from EXIF GPS coordinates.
- **Immich-Compatible REST Subset (ADR-0001)**:
  - `GET /api/server/ping` → `{"res":"pong"}` (unauthenticated healthcheck)
  - `GET /api/server/version` → `{"major":1,"minor":132,"patch":0}`
  - `POST /api/search/metadata` (and `POST /api/search/smart` aliased to metadata, ignoring ML query text) → supports `takenAfter`, `takenBefore`, `type`, `size`, `page`, `order`; returns Immich-shaped `{ assets: { items: [...], total, count, nextPage } }` with `exifInfo` (`dateTimeOriginal`, `city`, `state`, `country`) and `album` metadata.
  - `POST /api/search/random` → supports `takenAfter`, `takenBefore`, `type`, `size`; returns a randomized array of Immich-shaped asset objects.
  - `GET /api/memories` → accepts `for` ISO date; returns grouped memory buckets for that month/day across prior years.
  - `GET /api/assets/{id}` → returns single Immich-shaped asset object.
  - `GET /api/assets/{id}/thumbnail` → streams the cached 2K EXIF-oriented JPEG `Preview` with `Cache-Control` and `ETag`.
  - `POST /api/admin/scan` → triggers an asynchronous or synchronous incremental `Scan` (protected by `API_KEY`).
- **Bounded LRU Disk Cache**: Previews are stored in the writable data volume under a sharded cache directory, tracked with access timestamps, and pruned whenever total cache size exceeds `CACHE_MAX_MB` (default `1024`). Concurrent requests for the same uncached asset coalesce via singleflight.
- **Date Extraction Precedence**: EXIF `DateTimeOriginal` → EXIF `CreateDate` → filename date regex (`YYYYMMDD` / `YYYY-MM-DD`) → parent path date regex (`YYYY/MM` or `YYYY`) → file `mtime`.
- **Album Derivation**: Immediate parent directory name relative to `Library` root; set to empty if at library root or if the folder name is purely numeric (e.g., `2023`, `07`, `2023-07`).
- **MagicMirror Container**: Thin Docker image based on `karsten13/magicmirror` installing pinned releases of `MMM-ImmichSlideShow`, `MMM-Worldclock`, and `MMM-OpenWeatherMapForecast`. Mounts user `config.js` and `custom.css` from host, and connects to `http://photo-api:2283` over the private Compose bridge network.

## Testing Decisions

- **Testing Philosophy**: Test observable external behavior through the highest-level seam possible—do not assert on internal helper functions, SQL string formatting, or private structs.
- **Primary Seam (End-to-End HTTP + Real Temporary Library & SQLite)**:
  - One high-level integration seam spins up the real `photo-api` HTTP handler wired to a real temporary `Library` directory on disk, a real temporary `/data` directory (real SQLite DB + real LRU cache), and a small test GeoNames fixture (or bundled `cities1000`).
  - Tests drop real synthetic images (JPEG with EXIF date/GPS/orientation, PNG without EXIF, filename-dated images, files inside `@eaDir`/`#recycle`, numeric vs. named album folders, and optionally a sample HEIC in Docker) into the temp `Library`, trigger a `Scan`, and exercise the full HTTP contract (`ping`, `version`, `search/metadata`, `search/smart`, `search/random`, `memories`, `assets/:id`, `assets/:id/thumbnail`, auth rejection, incremental add/modify/delete, and LRU cache cap eviction).
- **Secondary Seam (Container Smoke Test)**:
  - Build and run the Docker image(s) against a mounted read-only fixture folder as a non-root `UID:GID` to verify `heif-convert`, bundled GeoNames, read-only permissions, and `/api/server/ping` healthcheck end-to-end.
- **Prior Art**: Greenfield Go service; standard Go `testing` + `net/http/httptest` table-driven tests running cleanly via `go test ./...` (and inside Docker build/test).

## Out of Scope

- Machine-learning / CLIP semantic search, facial recognition, people clustering, or object tagging.
- Video playback, video transcoding, or camera RAW (`.cr2`, `.dng`, `.arw`) decoding.
- Synology Photos virtual albums, user accounts, photo uploads, editing, or deletion via API.
- Multi-user authentication or web gallery UI (the service is strictly headless for `MMM-ImmichSlideShow`).
- Modifying the legacy `digital-photoframe` repository.

## Further Notes

- Once `gh auth login` is completed and a GitHub remote is added to `photoframe`, publish this spec to GitHub Issues with the `ready-for-agent` label per `docs/agents/issue-tracker.md`.
- Keep `digital-photoframe` untouched as a reference until the new `photoframe` stack is verified live on the Synology NAS and Raspberry Pi 3B.
