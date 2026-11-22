# PRD: Photoframe (Lightweight NAS Photo API + MagicMirror + Pi 3B Client)

## 1. Overview & Motivation

Running full **Immich** (PostgreSQL, Redis, machine-learning containers, transcoders) on a Synology NAS solely to power a family digital photo frame is heavy and unnecessary. Meanwhile, Synology Photos has no official API, breaks across DSM updates, and caps thumbnails at ~1280px—which looks soft on a **27-inch 2K (2560×1440)** monitor.

**Photoframe** replaces Immich with a single, unified repository containing:
1. **`photo-api/`**: A lightweight pure-Go + SQLite + offline GeoNames service that reads Synology photo folders directly (**read-only**), caches **2K (2560×1440)** JPEG previews within a **1GB LRU cap**, and exposes the exact Immich REST subset required by `MMM-ImmichSlideShow`.
2. **`magicmirror/`**: A thin Docker image extending `karsten13/magicmirror` with pinned modules (`MMM-ImmichSlideShow`, `MMM-Worldclock`, `MMM-OpenWeatherMapForecast`), secret-free sample configuration, and custom CSS.
3. **`client/`**: The proven Raspberry Pi 3B X11 kiosk setup (unified systemd schedule, event-driven GPIO PIR motion sensor, journald logging).
4. **Root `docker-compose.yml`**: One Synology Container Manager Project running `magicmirror` and `photo-api` together on a private internal Docker network.

---

## 2. System Architecture

```mermaid
flowchart LR
    subgraph NAS["Synology NAS (Container Manager — single docker-compose.yml)"]
        direction TB
        LIB[("Synology Photos Folder<br/>/volume1/photo<br/>(mounted :ro)")]
        DATA[("Writable Data Volume<br/>/data/index.db (SQLite)<br/>/data/cache/ (1GB LRU)")]
        GEO[("Embedded GeoNames<br/>cities1000 (~150k cities)")]

        subgraph NET["Private Internal Docker Network (photoframe_net)"]
            API["photo-api (Go, CGO_ENABLED=0)<br/>Port 2283 (internal only)<br/>• Incremental Scanner<br/>• EXIF + Offline Reverse Geo<br/>• Lazy 2K JPEG Preview + heif-convert"]
            MM["magicmirror (karsten13/magicmirror)<br/>Port 8080 → Host 8036<br/>• MMM-ImmichSlideShow<br/>• MMM-Worldclock<br/>• MMM-OpenWeatherMapForecast"]
        end

        LIB -->|Read-Only :ro| API
        DATA <-->|Index & 1GB Cache| API
        GEO -->|In-Memory Spatial Index| API
        MM <-->|http://photo-api:2283<br/>x-api-key| API
    end

    subgraph PI["Raspberry Pi 3B (Frame Client)"]
        direction TB
        SCHED["systemd digitalframe.service<br/>Display Schedule (30s poll)"]
        PIR["Event-Driven PIR Sensor<br/>GPIO 24 (5m idle timeout)"]
        DISP["MagicMirror Client-Only (X11)<br/>27-inch 2K Monitor (2560×1440)"]

        SCHED --> DISP
        PIR -->|vcgencmd on/off| DISP
    end

    DISP <-->|LAN HTTP :8036<br/>2K JPEGs (~400KB)| MM
```

---

## 3. Repository Layout

Each top-level folder owns its own `README.md` setup guide:

```text
self-host-digital-photo-frame/
├── README.md                    # System overview & links to server/ and client/ guides
├── PRD.md                       # This document
├── AGENTS.md                    # Agent instructions & skills pointers
├── CONTEXT.md                   # Canonical domain glossary
├── docs/
│   ├── adr/                     # Architectural Decision Records (0001–0004)
│   ├── agents/                  # Issue tracker, triage labels, domain doc rules
│   └── specs/                   # Engineering specifications (/to-spec)
├── server/
│   ├── README.md                # Synology NAS Container Manager setup guide
│   ├── docker-compose.yml       # Single stack running magicmirror + photo-api
│   ├── .env.example             # DSM PUID/PGID, volume paths, API_KEY, 2K & cache settings
│   ├── photo-api/
│   │   ├── README.md            # photo-api env vars, endpoints, tests
│   │   ├── Dockerfile           # Multi-stage pure Go + libheif-examples + GeoNames cities1000
│   │   ├── go.mod
│   │   ├── scripts/smoke-test.sh
│   │   └── ...                  # Go source & tests
│   └── magicmirror/
│       ├── README.md            # MagicMirror config, modules, secrets handling
│       ├── Dockerfile           # Thin pinned image extending karsten13/magicmirror
│       ├── config/
│       │   └── config.js.example
│       └── css/
│           └── custom.css
└── client/
    ├── README.md                # Raspberry Pi 3B installation, PIR wiring, schedule, logs
    ├── setup_client.sh
    ├── config.conf.sample
    ├── magicmirror-manager.sh
    ├── mm.sh
    ├── pir-control-display/
    └── systemd/
```

---

## 4. Performance Optimization Strategy

### Synology NAS (`photo-api`)
1. **Ultra-Low Idle Footprint**: Single statically linked pure-Go binary (`CGO_ENABLED=0`, `modernc.org/sqlite`) uses ~20–40MB RAM at rest (vs. 2–4GB+ for Immich + Postgres + Redis + ML).
2. **Zero-Waste Incremental Scans**: Diffs files by `(relative_path, mtime, size)` before ever opening a file. Unchanged libraries of 50,000+ assets re-scan in seconds with no disk thrashing.
3. **Skip Synology System Junk**: Hard-skips `@eaDir`, `#recycle`, and hidden `.*` directories so Synology's own hidden thumbnails are never indexed.
4. **Indexed SQLite Queries (WAL Mode)**: Composite indexes on `taken_at`, `(taken_month, taken_day)`, and `album` keep `anniversary`, `memory`, `random`, and year-slice `search` queries under **5ms**.
5. **Sub-Millisecond Offline Geocoding**: GeoNames `cities1000` (~150k worldwide cities) is bundled in the Docker image and loaded into a fast in-memory spatial grid/KD-tree—zero external API calls, <1ms lookup per asset during scan.
6. **Lazy 2K Previews + Bounded 1GB LRU Disk Cache**:
   - No upfront CPU burn during scans; previews are generated only when requested (~150–400ms first hit, <5ms cached).
   - `heif-convert` runs only once per `.heic`/`.heif` asset and is cached as JPEG thereafter.
   - Singleflight deduplication prevents concurrent requests for the same uncached asset from duplicating resize work.
   - Background LRU eviction keeps `/data/cache` strictly under `CACHE_MAX_MB` (default **1024 MB**, ~2,000–2,500 2K JPEGs).

### Raspberry Pi 3B (`client`)
1. **Native 2K Sizing (~350–500KB JPEG)**: Downscaling 10–25MB smartphone originals/HEICs on the NAS to 2560×1440 JPEG (quality 88) cuts Pi 3B network transfer and decode RAM by ~20–40× while staying 1:1 sharp on the 27" 2K monitor.
2. **JPEG Over WebP**: Faster decode on Pi 3B's ARM CPU and full compatibility with `MMM-ImmichSlideShow`'s `showBlurredImageForBlackBars`.
3. **HTTP Cache Headers**: `Cache-Control: public, max-age=86400, immutable` (keyed by asset ID + mtime) lets the Pi's Chromium cache repeat slides cheaply without storing library data on the SD card.
4. **Existing Pi 3B Optimizations Preserved**: Forced X11 mode, event-driven GPIO PIR motion sensor (0% idle CPU polling), 5-minute display power-off, and 64MB-capped journald logging.

---

## 5. Security & Synology Permissions
- **Read-Only Library (`:ro`)**: Original photos can never be modified or deleted by the container.
- **Non-Root DSM `PUID:PGID`**: Container runs as `user: "${PUID:-1026}:${PGID:-100}"` matching Synology DSM ACLs, with startup readability/writability checks that print actionable permission errors.
- **Network Isolation**: `photo-api` runs only on the internal Docker bridge network (`http://photo-api:2283`) with optional `x-api-key` authentication; no LAN port exposed by default.
- **Zero Secrets in Git**: `config.js` and `.env` are gitignored; only `.example` templates are committed.

---

## 6. Acceptance Criteria (v1)
- `docker compose up -d --build` starts both `photo-api` and `magicmirror` cleanly on Synology Container Manager (and local Docker) as a non-root `PUID:PGID`.
- `MMM-ImmichSlideShow` displays photos in `anniversary` (±45 days across years), `memory`, `random`, and date-range `search` modes with accurate `date`, `geo` (worldwide cities), `count`, and `album` overlays.
- Both JPEG/PNG/WebP and iPhone `.heic` photos render crisp at 2560×1440 with correct EXIF orientation.
- Disk cache stays within the configured 1GB cap under sustained traffic.
- `client/`, `magicmirror/`, and `photo-api/` each have a complete, standalone `README.md`.
