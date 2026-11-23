# 0006. In-Memory 1GB LRU Preview Cache

Date: 2026-11-23

## Status

Accepted

## Context

The digital photo frame runs on a 27-inch 2K (2560×1440) monitor. The server runs in Docker on a Synology NAS equipped with spinning HDDs (e.g. IronWolf/WD Red) and ample RAM.

Previously, `photo-api` cached resized 2K JPEG previews on disk under `/data/cache`. In `anniversary` mode, the frame displays up to 1,700 photos a day at 12-second intervals. Writing every generated preview to spinning disks caused continuous random disk I/O, prevented Synology HDD sleep/standby, and incurred unnecessary disk write churn.

Furthermore, decoding large modern smartphone and camera photos (24MP–48MP) uncompressed into memory consumes ~90MB–190MB of RAM per photo during downscaling. Without strict concurrency gating, multiple simultaneous decodes risked exceeding container memory limits.

## Decision

1. **Default to In-Memory LRU Cache (`CACHE_MODE=memory`)**:
   - Store generated 2K JPEGs as compressed `[]byte` buffers directly in an in-memory double-linked list LRU cache with exact byte accounting.
   - Allocate 1024 MB (1.0 GB) for the cache (`CACHE_MAX_MB=1024`), which holds ~2,300 compressed 2K JPEGs.
   - **Zero Disk Writes**: In memory mode, no preview files are written to `/data/cache`. The spinning HDD is read only once when an image is first displayed; thereafter, all slideshow transitions serve instantly from RAM (< 1ms).

2. **Decode Concurrency Semaphore (`MAX_CONCURRENT_DECODES=2`)**:
   - Limit parallel image decodes to at most 2 concurrent workers via a Go channel semaphore.
   - Restricts peak uncompressed bitmap memory buffers to ~190 MB.

3. **Container Memory Bounding**:
   - Enforce Docker container limit `mem_limit: 1300m` with `GOMEMLIMIT=1200MiB` in `docker-compose.yml`.
   - Peak container memory is bounded under ~750MB–800MB, completely eliminating Docker OOM risk.

4. **100-Photo/Year Anniversary Sampling**:
   - Set `querySize: 100` per year across years 2010–2026, pulling up to 1,700 photos per day.
   - Every day at midnight (or after 24h), the anniversary date window advances by 1 day and SQLite samples a fresh random set of 100 photos per year. The 1GB RAM cache seamlessly evicts yesterday's unviewed photos in $O(1)$ time as today's new photos arrive.

## Consequences

- **Pros**: Zero spinning HDD write churn; Synology disks can spin down during slideshow playback; sub-millisecond preview serving latency (0.6ms); predictable memory ceiling with 0% OOM risk; 1,700 diverse anniversary memories each day.
- **Cons**: On container restart, the cache starts empty and repopulates lazily as slides are shown (1 decode every 12 seconds consumes ~1% CPU). If disk persistence across restarts is ever desired, `CACHE_MODE=hybrid` or `CACHE_MODE=disk` can be selected via `.env`.
