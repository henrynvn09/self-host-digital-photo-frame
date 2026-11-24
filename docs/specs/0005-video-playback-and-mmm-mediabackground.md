# Spec 0005: Video Asset Playback, Bounded Playback Window & `MMM-MediaBackground`

## Problem Statement

Family media libraries stored on a Synology NAS contain thousands of memorable video moments alongside still photographs, yet the digital frame currently ignores all video files during library indexing and slideshow display. Even if videos were naively enabled in a standard browser `<video>` tag, playing raw smartphone recordings (such as multi-minute 4K 60 fps 10-bit HEVC `.mov` and `.mp4` files) directly on a 1 GB Raspberry Pi 4 `Frame Client` (`948 MB` ARM / `76 MB` GPU split running X11 in `1440×2560` portrait mode) would:

1. Stall the slideshow for minutes at a time whenever a long vacation or birthday recording is encountered.
2. Disrupt a quiet living space if unmuted audio suddenly plays on an unattended wall frame.
3. Require installing heavy GStreamer codec pipelines on the `Frame Client` and risk WebKitGTK media buffer memory leaks or GPU exhaustion across multi-day kiosk uptime.
4. Hammer the `76 MB` GPU if CSS `backdrop-filter: blur()` recomputes full-screen `1440×2560` letterbox bars 30–60 times per second, while third-party `MMM-ImmichSlideShow` already holds three full-screen layers in the DOM simultaneously and lacks video support altogether.
5. Pollute the slideshow with accidental 1.5–3 second iPhone Live Photo companion clips looping rapidly like broken boomerangs.

## Solution

Extend the system end-to-end to index, prepare, cache, and display video `Asset`s seamlessly alongside photo `Asset`s while pushing 100% of video decoding, rotation, audio stripping, frame-rate capping (`30 fps`), blurred backdrop generation, and `Playback Window` timing onto the Synology NAS:

1. **Configurable `Playback Window` (Defaulting to Photo Slide Interval)**:
   - Viewers declare an optional video `Playback Window` (defaulting automatically to the configured photo slide change interval, e.g. 12 seconds).
   - If a video `Asset` is **longer** than the `Playback Window`, only its opening segment up to the `Playback Window` is played before advancing.
   - If a video `Asset` is **shorter** than the `Playback Window`, the clip loops seamlessly until the full `Playback Window` elapses before advancing.
   - All video playback is silent by default (`videoMuted: true`), with audio tracks stripped server-side when muted.
2. **Smart Video `Scan` & Live Photo Companion Filtering**:
   - Incremental `Scan` indexes `.mp4`, `.mov`, `.m4v`, and `.webm` `Asset`s using `ffprobe` once per new or modified video to capture duration, QuickTime creation timestamp, dimensions, rotation, and ISO-6709 GPS coordinates (resolved offline via GeoNames into city, state, and country).
   - `Scan` automatically skips micro-clips shorter than a minimum duration (`3s` default) as well as paired Live Photo companion clips (`<= 3.5s` sharing directory and filename stem with an image `Asset`).
   - Both `anniversary` (`Cyclic Slice`s over the `Anniversary Permutation`) and `random` modes mix photo and video `Asset`s naturally by default while supporting optional media-type filtering (`IMAGE` or `VIDEO`).
3. **Synology Offloading & Zero-Touch `Frame Client` Streaming**:
   - While Slide `N` is on screen, lookahead prefetch warms Slide `N+1` on the NAS (`photo-api`). `ffmpeg` transcodes only the opening `Playback Window` into a silent, auto-rotated, unpadded `1080p`-max `H.264` `Preview` capped at `min(30, source_fps)` (~2 MB) and generates a tiny (~30 KB) static pre-blurred, darkened JPEG backdrop—storing both in the existing `1024 MB` in-memory LRU cache with zero spinning-disk writes.
   - When the video slide becomes active, `photo-api` reads the cached ~2 MB clip from RAM and paces a 30 fps `multipart/x-mixed-replace` MJPEG HTTP stream (looping server-side for the full `Playback Window`) through MagicMirror's zero-dependency stream pipe directly into a standard `<img>` element over the static pre-blurred backdrop.
   - The `Frame Client` requires **zero new OS packages**, **zero GStreamer codecs**, **zero `<video>` elements**, and **zero per-frame CSS blur shaders**, rendering video through WebKitGTK's native C++ `libjpeg-turbo` `<img>` path with zero extra GPU memory.
4. **First-Party `MMM-MediaBackground` Module**:
   - Replace third-party `MMM-ImmichSlideShow` with a lean first-party `MMM-MediaBackground` module featuring a strict 2-slot DOM footprint, clean `Live Config` settings (with backwards-compatible fallback for legacy `immichConfigs`), `N+1` prefetching, cold-cache hold grace (`+8s` max), automatic skip on corrupt media, and bottom-corner metadata overlays (`date`, `geo`, `count`, `album`, plus a `🎥` video duration indicator).

## User Stories

### Bounded Playback Window & Pacing
1. As a frame viewer, I want video `Asset`s to play directly in the fullscreen background slideshow alongside photo `Asset`s, so that family memories with motion come alive on the wall monitor.
2. As a frame operator, I want the video `Playback Window` to default automatically to the configured photo slide interval (e.g. 12 seconds) when not explicitly declared, so that video support works out of the box with consistent slideshow pacing.
3. As a frame operator, I want to optionally declare a custom `Playback Window` (via milliseconds or a seconds convenience setting) distinct from the photo interval, so that I can allow videos to play slightly longer or shorter than still photos whenever desired.
4. As a frame viewer, I want any video longer than the `Playback Window` (e.g. a 2-minute birthday video when the window is 12 seconds) to play only its first $X$ seconds and then transition cleanly to the next `Asset`, so that long recordings never monopolize the frame.
5. As a frame viewer, I want any indexed video shorter than the `Playback Window` (e.g. a 5-second clip when the window is 12 seconds) to loop continuously until the full $X$-second `Playback Window` has elapsed, so that brief clips remain on screen long enough to appreciate.
6. As a household member, I want all video `Asset`s to play muted by default with audio stripped on the server, so that an unattended wall frame never startles the room with sudden sound.
7. As a frame operator, I want `Live Config` edits to slide speed or `Playback Window` settings to take effect dynamically on connected displays without manually rebuilding containers or clearing stale disk caches, so that tuning slideshow pacing stays effortless.

### Library Indexing, Metadata & Live Photo Filtering
8. As a NAS owner, I want incremental `Scan`s to index `.mp4`, `.mov`, `.m4v`, and `.webm` video `Asset`s from the read-only `Library` without ever modifying or writing into source directories, so that original family media remains 100% untouched.
9. As a frame viewer, I want portrait and upside-down smartphone videos (recorded with 90°, 180°, or 270° QuickTime rotation metadata) to appear right-side up automatically, so that vertical phone videos never play sideways.
10. As a frame viewer, I want video `Asset`s to display accurate capture dates extracted from QuickTime/MP4 creation timestamps (falling back to filename or directory dates and then modification time), so that historical videos sort into the right anniversary years and show meaningful date badges.
11. As a frame viewer, I want video `Asset`s containing ISO-6709 GPS coordinates to resolve offline into city, state, and country labels via GeoNames, so that vacation clips display the same location overlay as photos.
12. As a frame viewer, I want video `Asset`s inside named event folders to derive their `Album` label using the exact same non-numeric parent-folder rule as photos, so that trip names appear consistently across all media.
13. As an iPhone user backing up to Synology Photos, I want `Scan` to automatically skip short paired Live Photo companion clips (`<= 3.5s` sharing directory and filename stem with an image `Asset`) and micro-clips under a configurable minimum duration (`3s` default), so that accidental 1.5-second Live Photo clips do not loop 8 times in a row or duplicate still photos.
14. As a NAS owner, I want unchanged video `Asset`s (matching relative path, modification time, and byte size) to skip `ffprobe` inspection on subsequent incremental `Scan`s, so that periodic 30-minute `Scan` passes remain fast and lightweight.
15. As a frame viewer, I want both `anniversary` (`Cyclic Slice`s of the `Anniversary Permutation`) and `random` slideshow modes to return a natural unified mix of photo and video `Asset`s by default, so that videos from past years surface organically alongside photos from the same trip or season.
16. As a frame operator, I want each slideshow source configuration to optionally filter by media type (`IMAGE`, `VIDEO`, or all), so that I can create photo-only or video-only rotation blocks if desired.

### Synology Offloading, Caching & Raspberry Pi Performance
17. As a Raspberry Pi 4 owner with 1 GB RAM (`948 MB` ARM / `76 MB` GPU) running X11 in `1440×2560` portrait mode, I want video frames capped at `30 fps` (preserving lower native frame rates like `24 fps` without judder while down-converting `60/120 fps` sources to `30 fps`), so that client CPU usage stays low and smooth.
18. As a Raspberry Pi 4 operator, I want video playback to work inside the existing minimal WebKitGTK `Frame Client` without installing GStreamer codec packages or creating HTML5 `<video>` pipelines, so that existing Pi installations require zero `apt` changes and remain immune to browser media-pipeline memory leaks.
19. As a Raspberry Pi 4 operator, I want letterbox bars behind unpadded `1080p`-max video `Preview`s to use a static server-blurred, darkened JPEG backdrop rather than client-side CSS `backdrop-filter: blur()`, so that the Pi's `76 MB` GPU performs zero per-frame blur shader computations and Synology encodes up to 68% fewer video pixels on landscape clips.
20. As a NAS owner with spinning HDDs, I want transcoded video `Preview`s (~2 MB compact MP4 clips) and static blurred backdrops (~30 KB JPEGs) stored in `photo-api`'s existing `1024 MB` in-memory LRU cache (`CACHE_MODE=memory`), so that slideshow playback performs zero disk writes and lets NAS hard drives spin down once cached.
21. As a frame viewer, I want `MMM-MediaBackground` to prefetch Slide `N+1` on `photo-api` as soon as Slide `N` appears on screen, so that `ffmpeg` finishes transcoding upcoming uncached videos in the background during Slide `N`'s display interval and transitions happen instantaneously.
22. As a NAS operator, I want `photo-api` container memory headroom raised to `2048 MB` and background `ffmpeg` transcodes serialized (`MAX_CONCURRENT_VIDEO_TRANSCODES=1`, `-threads 2`), so that decoding 4K60 HEVC videos outside Go's `GOMEMLIMIT` heap can never trigger a Linux cgroup OOM kill when the 1 GB RAM cache is full.
23. As a frame viewer, I want Slide `N` to remain cleanly on screen for a bounded grace period (up to `+8s`) if a cold-cache 4K video in Slide `N+1` is still finishing transcoding when the slide timer fires, so that the display never flashes black or drops newly encountered videos unnecessarily.
24. As a frame viewer, I want any corrupt, unreadable, or timed-out video `Asset` to be logged, negative-cached briefly, and skipped automatically in favor of Slide `N+2`, so that a broken file in the `Library` never stalls or crashes the slideshow.
25. As a frame viewer, I want closing or advancing away from a video slide on the `Frame Client` to immediately terminate the HTTP MJPEG stream end-to-end back to `photo-api`, so that Synology stops pacing frames the instant a slide leaves the screen.

### `MMM-MediaBackground` Display, Security & Overlays
26. As a Raspberry Pi 4 operator, I want `MMM-MediaBackground` to maintain at most a strict 2-slot transition DOM structure (retiring `MMM-ImmichSlideShow`'s 3-layer full-screen GPU texture footprint and third-party `http-proxy-middleware` bloat), so that browser GPU and heap usage stay minimal across weeks of continuous operation.
27. As a security-conscious NAS operator, I want `MMM-MediaBackground`'s server helper to pipe still `Preview`s, blurred backdrops, and chunked MJPEG video streams from internal `photo-api` using Node's built-in `http` streaming client while injecting `x-api-key` server-side, so that `photo-api` stays unexposed on the LAN and credentials never reach the browser.
28. As a frame operator migrating from `MMM-ImmichSlideShow`, I want `MMM-MediaBackground` to offer clean first-party configuration keys (`sources`, `slideshowSpeed`, `videoSlideshowSpeed`, `maxVideoDurationSeconds`, `videoFps`, `videoMuted`, `infoFields`) while still accepting legacy `immichConfigs` and `imageInfo` keys as seamless aliases, so that configuration stays clean and upgrades are frictionless.
29. As a frame viewer, I want video slides to render the same crisp metadata overlay (`date`, `geo`, `count`, `album`) as photo slides plus a subtle `🎥` duration badge (e.g. showing played window vs total clip length when trimmed, or clip length when looped), so that viewers know at a glance they are watching a video memory and how long the original moment was.
30. As a frame viewer, I want Clock, Worldclock, Calendar, Google Sheets To-Do, and Weather `Widget Feed` overlays to remain crisp native 2K HTML text rendered cleanly above `MMM-MediaBackground` during both photo and video playback.

## Implementation Decisions

- **Media Indexing & `ffprobe` Metadata Extraction (`photo-api` Scanner & Store)**:
  - Expand supported `Library` extensions to include `.mp4`, `.mov`, `.m4v`, and `.webm` (case-insensitive) alongside existing still image extensions.
  - Extend the SQLite `assets` schema with backward-compatible migrations (`ALTER TABLE` when absent) adding `media_type TEXT NOT NULL DEFAULT 'IMAGE'` (`'IMAGE'` | `'VIDEO'`) and `duration_sec REAL NOT NULL DEFAULT 0`.
  - During incremental `Scan`, inspect new or changed video files via `ffprobe` JSON output (with a per-file subprocess timeout) to extract:
    - Exact `duration_sec`.
    - Display dimensions (`width`, `height`) accounting for stream rotation matrices / `displaymatrix` side data (`90°` / `270°` swap width and height; normalize `orientation` to `1` since `ffmpeg` auto-rotates output).
    - Capture timestamp from `format.tags.com.apple.quicktime.creationdate` or `creation_time` (falling back to filename date, path year/month, and file `mtime`).
    - Geographic coordinates parsed from ISO-6709 tags (`com.apple.quicktime.location.ISO6709` or `location`, e.g. `+33.8992-118.0689/`), resolved through the existing offline GeoNames resolver into `city`, `state`, and `country`.
  - **Live Photo & Micro-Clip Exclusion Rule**:
    - Configure `MIN_VIDEO_SECONDS` (env var, default `3.0`) and `LIVE_PHOTO_MAX_SECONDS` (`3.5`).
    - During `Scan` discovery of a directory, collect image basenames (stem without extension, case-insensitive). Skip any video file whose probed duration is `< MIN_VIDEO_SECONDS`, or whose stem matches a sibling image in the same directory and whose duration is `<= LIVE_PHOTO_MAX_SECONDS`. If a previously indexed video later falls under an updated rule, prune it cleanly like any removed asset.

- **Search & Metadata API Contracts (`photo-api` HTTP Server)**:
  - Update asset JSON serialization to emit actual `type` (`"IMAGE"` or `"VIDEO"`) and `durationSeconds` (`float64`, omitted or `0` for images) alongside existing metadata fields.
  - Update `POST /api/search/metadata`, `POST /api/search/smart`, and `POST /api/search/random` request parsing:
    - `type: ""` or `"ALL"` returns both `IMAGE` and `VIDEO` `Asset`s from the unified SQLite pool (preserving ADR-0007 / Spec 0004 deterministic multi-round Fisher-Yates `Anniversary Permutation` without database writes).
    - `type: "IMAGE"` filters `WHERE media_type = 'IMAGE'`; `type: "VIDEO"` filters `WHERE media_type = 'VIDEO'`; unknown values return an empty result set.
  - `GET /api/memories` returns both `IMAGE` and `VIDEO` `Asset`s matching the target month/day.

- **Video `Preview`, Static Blurred Backdrop & MJPEG Stream Engine (`photo-api` Preview Cache & Runtime)**:
  - Bundle `ffmpeg` (and `ffprobe`) inside the `photo-api` runtime container image alongside `heif-convert`, keeping the Go binary `CGO_ENABLED=0` (consistent with ADR-0003).
  - Add concurrency and sizing configuration:
    - `VIDEO_MAX_WIDTH` / `VIDEO_MAX_HEIGHT` (default `1920`×`1080` landscape / `1080`×`1920` portrait bounding box, i.e., `1080p`-max unpadded aspect-fit with even dimensions `trunc(iw/2)*2`).
    - `VIDEO_FPS` (default `30`).
    - `MAX_CONCURRENT_VIDEO_TRANSCODES` (default `1`, gated by a dedicated channel semaphore independent of photo decodes, invoking `ffmpeg -threads 2`).
    - Increase the `photo-api` service container memory and swap ceiling from `1300m` to `2048m` while keeping `CACHE_MAX_MB=1024` and `GOMEMLIMIT=1200MiB`.
  - **Cache Entries in Existing 1 GB In-Memory LRU (`CACHE_MODE=memory`)**:
    - Key video clip renditions by `(assetID, mtime, windowSec, fps)` so changing the `Playback Window` in `Live Config` automatically keys a fresh rendition without manual cache invalidation.
    - Singleflight deduplication ensures concurrent `/warm` prefetch and live `/stream` requests for the same key share one in-flight `ffmpeg` run rather than spawning duplicate processes.
    - **Transcode Step**: `ffmpeg` reads at most `min(assetDuration, windowSec)` seconds (`-t`), strips audio (`-an`), applies auto-rotation, caps frame rate via `fps=min(targetFps, source_fps)`, scales unpadded to fit within the `1080p` bounding box, and outputs a compact `H.264` (`yuv420p`) MP4 buffer (~1.5–3 MB) stored directly in the RAM LRU cache.
    - **Static Pre-Blurred Backdrop (`GET /api/assets/{id}/backdrop`)**: For both video and photo `Asset`s (or specifically requested by `MMM-MediaBackground`), extract a representative still frame (first keyframe at `~0.1s` or `0s` for videos, decoded image for photos), downscale + box-blur + darken (~35%) into a compact (`~360×640` or `480×854` upscaled cleanly by browser `background-size: cover`) ~25–40 KB JPEG cached in the RAM LRU. This eliminates client-side CSS `backdrop-filter: blur()` completely for both photos and videos if used, and guarantees zero GPU shader cost during 30 fps video playback.
    - **Prefetch Endpoint (`POST /api/assets/{id}/warm?window=<sec>&fps=<fps>`)**: Warms both the blurred backdrop JPEG and (for `VIDEO` `Asset`s) the bounded MP4 clip into the LRU cache asynchronously or blocking until warm, returning `200 OK` when ready or `422`/`500` if the source file is corrupt (storing a short-TTL negative-cache entry, e.g. 10 minutes, so corrupt files fail fast without repeated `ffmpeg` retries).
    - **Server-Paced MJPEG Stream Endpoint (`GET /api/assets/{id}/stream?window=<sec>&fps=<fps>`)**:
      - Ensures the cached MP4 clip is warm in RAM, then streams `Content-Type: multipart/x-mixed-replace; boundary=frame` (`Cache-Control: no-store`).
      - Decodes the cached MP4 clip from memory via a lightweight `ffmpeg` pipe (`-stream_loop -1 -t <windowSec> -i pipe:0 -f mpjpeg` or paced frame emitter synced to wall-clock monotonic ticks at `1 / effectiveFps`) so clips shorter than `windowSec` loop seamlessly until `windowSec` elapses, and clips already trimmed to `windowSec` play once across `windowSec`.
      - Honors `r.Context().Done()` immediately: when the `Frame Client` switches `img.src` or sleeps, the HTTP connection closes and the pacing subprocess/goroutine terminates immediately.
    - `GET /api/assets/{id}/thumbnail` for a `VIDEO` `Asset` also returns a still JPEG poster frame (cached in LRU) so legacy or direct thumbnail callers never fail on video IDs.

- **First-Party `MMM-MediaBackground` MagicMirror Module**:
  - Build `MMM-MediaBackground` as a bundled first-party MagicMirror module with zero third-party `npm` dependencies (using Node's built-in `http`/`https` and `URL` modules), and retire the third-party `MMM-ImmichSlideShow` clone from the MagicMirror image build.
  - **Configuration Schema Shape & Backwards Compatibility** (canonical config shape derived during design synthesis):
    ```js
    {
      module: "MMM-MediaBackground",
      position: "fullscreen_below",
      config: {
        slideshowSpeed: 12000,
        videoSlideshowSpeed: null,       // ms; defaults to slideshowSpeed when null/omitted
        maxVideoDurationSeconds: null,   // optional alias in seconds (overrides videoSlideshowSpeed if set)
        videoFps: 30,
        videoMuted: true,
        prefetchNext: true,
        coldCacheHoldMs: 8000,           // max extra hold on Slide N while Slide N+1 warms
        transitionSpeed: "1.2s",
        backgroundSize: "contain",
        showBlurredBackdrop: true,
        showMediaInfo: true,             // accepts showImageInfo alias
        infoFields: ["date", "geo", "count", "album"], // accepts imageInfo alias
        dateFormat: "DD-MM-YYYY dddd HH:mm",
        sources: [                       // accepts immichConfigs alias
          {
            url: "http://photo-api:2283",
            apiKey: "...",
            mode: "anniversary",         // "anniversary" | "random" | "metadata"
            anniversaryDatesBack: 45,
            anniversaryDatesForward: 45,
            anniversaryStartYear: 2010,
            anniversaryEndYear: 2026,
            querySize: 100,
            mediaType: "ALL"             // "ALL" | "IMAGE" | "VIDEO" (also honors query.type)
          }
        ]
      }
    }
    ```
  - **Zero-Dependency Internal Stream Pipe (`node_helper`)**:
    - Registers Express routes `/mediabackground/preview/:id`, `/mediabackground/backdrop/:id`, and `/mediabackground/stream/:id` on MagicMirror's internal Express app.
    - Pipes requests directly to `photo-api` via `http.request`, injecting `x-api-key`, forwarding `Content-Type` (`image/jpeg` or `multipart/x-mixed-replace; boundary=frame`), disabling response buffering (`res.flushHeaders()`), and aborting the upstream request immediately on client `req.on("close")`.
  - **Lookahead Prefetch, Cold-Cache Hold & Corrupt Skip State Machine (`node_helper`)**:
    - Immediately after dispatching `DISPLAY_MEDIA` for Slide `N`, triggers `POST /api/assets/{nextId}/warm` for Slide `N+1` in the background.
    - When Slide `N`'s timer (`slideshowSpeed` for `IMAGE`, resolved `Playback Window` for `VIDEO`) expires:
      - If Slide `N+1` is already warm, dispatches `DISPLAY_MEDIA` immediately.
      - If Slide `N+1` warm is still in flight, waits up to `coldCacheHoldMs` (`8000` ms default) for warm completion before dispatching.
      - If Slide `N+1` warm fails or exceeds `coldCacheHoldMs`, logs a warning and advances immediately to Slide `N+2` (continuing until a valid slide is found, bounded by list length).
  - **Minimal 2-Slot Client DOM & Overlay (`MMM-MediaBackground` browser script)**:
    - Renders at most **one active slide container** (plus brief incoming crossfade slot removed immediately on `transitionend` / fallback timeout), each containing only:
      1. A static backdrop `<div>` (`background-image: url(/mediabackground/backdrop/:id)`, `background-size: cover`) — **no CSS `backdrop-filter`**.
      2. A single foreground `<img>` (`object-fit: contain`, `src` set to `/mediabackground/preview/:id` for `IMAGE` or `/mediabackground/stream/:id?window=...&fps=...&t=...` for `VIDEO`).
    - Before removing an outgoing slide (or when MagicMirror suspends the module), explicitly resets outgoing `img.src = ""` so WebKitGTK tears down any active MJPEG HTTP connection immediately.
    - Formats bottom-right metadata overlay (`date`, `geo`, `count`, `album`), appending `🎥 0:12 / 0:45` when a video is trimmed to its `Playback Window` or `🎥 0:05` when playing a shorter looping video.

## Testing Decisions

- **What Makes a Good Test**:
  - Tests must assert observable external behavior across module boundaries (HTTP status codes, JSON schemas, `multipart/x-mixed-replace` MJPEG frame boundaries, timing/looping semantics, cache byte accounting, zero disk writes in memory mode, and emitted socket/DOM states)—never private struct fields or internal helper names.

- **Seam 1 — `photo-api` End-to-End HTTP Seam (Go Integration Suite + Container Smoke Check)**:
  - **Prior Art**: Existing end-to-end HTTP integration suite driving `Server.Handler()` with a real temporary `Library`, real SQLite store, offline GeoNames fixture, and real in-memory LRU cache, plus the Docker non-root container smoke script.
  - **Behaviors Verified**:
    1. *Video `Scan` & Metadata*: Real `ffmpeg`-synthesized `.mp4`/`.mov` fixtures (including rotated portrait metadata and ISO-6709 GPS tags) are indexed as `type: "VIDEO"` with accurate `durationSeconds`, capture timestamps, rotated dimensions, GeoNames `city`/`country`, and `Album` names.
    2. *Live Photo & Micro-Clip Filtering*: Sub-3s clips and `<= 3.5s` `.mov` files sharing directory + stem with an image are excluded from the index, while standalone `>= 3s` videos (even sharing a folder with photos of different names, or same stem `> 3.5s`) are indexed.
    3. *Unified & Filtered Search*: `anniversary` (`GET /api/memories` and bounded `POST /api/search/random`) and unbounded `random` queries return both `IMAGE` and `VIDEO` `Asset`s by default, and strictly filter when `type: "IMAGE"` or `type: "VIDEO"` is passed.
    4. *Bounded `Playback Window` Trimming & Looping over MJPEG*: Requesting `/api/assets/{id}/stream?window=2&fps=10` on a `5s` fixture streams valid `multipart/x-mixed-replace` JPEG frames (~20 frames over ~2s) and terminates cleanly; requesting `window=3&fps=10` on a `1s` fixture loops seamlessly across boundaries (~30 valid JPEG frames) without error; canceling the request context stops frame emission promptly.
    5. *Warm Prefetch, Blurred Backdrop & Memory Mode*: `POST /api/assets/{id}/warm` populates both the compact MP4 clip and blurred JPEG backdrop (`GET /api/assets/{id}/backdrop`) in RAM with zero files written to disk in `memory` cache mode.
    6. *Corrupt Video Resilience*: An invalid/truncated `.mp4` file fails cleanly during warm/stream without crashing the server and hits fast negative cache on immediate retry.
    7. *Container Smoke Test*: Non-root (`1026:100`) Docker smoke check verifies both `ffmpeg` and `ffprobe` are executable alongside `heif-convert`.

- **Seam 2 — `MMM-MediaBackground` Module Boundary Seam (`node:test` Suite)**:
  - **Prior Art**: Existing zero-dependency `node:test` + `node:assert/strict` test suite used by `MMM-GoogleSheetsToDo`.
  - **Behaviors Verified**:
    1. *Config Normalization & Fallback Rules*: Verifies `videoSlideshowSpeed` defaults to `slideshowSpeed` when omitted, honors explicit `videoSlideshowSpeed` and `maxVideoDurationSeconds`, and normalizes legacy `immichConfigs` / `imageInfo` / `showImageInfo` aliases identically to `sources` / `infoFields` / `showMediaInfo`.
    2. *Zero-Dependency Stream Pipe*: Against an in-process Node `http.createServer` mock `photo-api`, verifies `/mediabackground/stream/:id` injects `x-api-key`, flushes `multipart/x-mixed-replace` chunks incrementally without buffering the whole stream, and aborts the upstream request immediately when the downstream client disconnects.
    3. *Prefetch, Hold Grace & Skip State Machine*: Verifies `N+1` `/warm` fires immediately on displaying Slide `N`, holds Slide `N` up to `coldCacheHoldMs` while `N+1` finishes warming, and skips straight to `N+2` when `N+1` returns an error or times out.
    4. *2-Slot Lifecycle & Video Badge Formatting*: Verifies outgoing `img.src` is cleared (`""`) on slide replacement/suspend and video metadata badges render `🎥` duration strings accurately for both trimmed and looped clips.

## Out of Scope

- Audio output routing (HDMI/ALSA/PulseAudio) on the Raspberry Pi `Frame Client` (videos are silent wall-frame memories by default).
- Hardware GPU transcoding (`/dev/dri` VAAPI/QuickSync) configuration inside Synology Container Manager (software `ffmpeg -threads 2` with `N+1` 12-second lookahead prefetch and 1 GB RAM caching easily keeps pace without privileged device mounts).
- Editing, trimming, or deleting source video files in the read-only NAS `Library`.
- Interactive touch/remote-control scrubbing, pause/seek bars, or playlist editors on the wall display.

## Further Notes

- Implements **ADR-0008** (*Replace `MMM-ImmichSlideShow` with `MMM-MediaBackground` and server-paced 30 fps MJPEG video*), partially superseding **ADR-0001** while preserving **ADR-0002** (read-only NAS `Library`), **ADR-0003** (`CGO_ENABLED=0` Go binary + CLI media tools), **ADR-0005** (mounted `Live Config` reload), **ADR-0006** (zero-disk-write 1 GB in-memory LRU cache), and **ADR-0007** (stateless deterministic `Anniversary Permutation`).
- Uses canonical domain language defined in `CONTEXT.md` (**Asset**, **Library**, **Scan**, **Preview**, **Album**, **Anniversary Permutation**, **Cyclic Slice**, **Exhaustion Cycle**, **Frame Client**, **Display Schedule**, **Widget Feed**, **Live Config**, **Playback Window**).
