# Use pure Go (CGO_ENABLED=0) with `heif-convert` subprocess for HEIC decoding

`server-photo-api` runs on Synology NAS hardware that spans both `linux/amd64` and `linux/arm64`. Using pure Go (`modernc.org/sqlite` for SQLite and standard/pure-Go codecs for JPEG, PNG, WebP) while invoking `heif-convert` only for `.heic`/`.heif` assets avoids CGO cross-compilation complexity and C memory-management bugs. Because generated 2K `Preview`s are cached on disk, `heif-convert` runs at most once per HEIC asset.

## Consequences

- Runtime Docker image must include `libheif-examples` (`heif-convert`).
- Uncached HEIC requests incur a small process-spawn overhead (~15–30ms), which is negligible compared to a 15–30s slideshow interval and zero on cache hits.
