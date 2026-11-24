---
status: accepted
---

# Replace `MMM-ImmichSlideShow` with `MMM-MediaBackground` and server-paced 30 fps MJPEG video

To play video assets smoothly on the 1 GB Raspberry Pi 4 (`948 MB` ARM / `76 MB` GPU split, X11 `1440×2560` portrait WebKitGTK kiosk) without adding GStreamer codecs on the Pi or risking WebKit `<video>` pipeline memory leaks, we replace third-party `MMM-ImmichSlideShow` (partially superseding ADR-0001) with a lean first-party `MMM-MediaBackground` module and offload all video decoding, rotation, audio stripping, duration trimming, and 30 fps pacing to the Synology NAS (`photo-api` + `ffmpeg`). `photo-api` transcodes only the opening `Playback Window` of each video into a compact unpadded `1080p`-max 30 fps H.264 clip stored in the 1 GB in-memory LRU cache (warmed via `N+1` slide prefetch) and streams it on playback as `multipart/x-mixed-replace` MJPEG (looping server-side until the window expires) into a standard `<img>` element over a static pre-blurred JPEG backdrop.

## Considered Options

- **HTML5 `<video>` with H.264 MP4 on the Pi**: Lowest LAN bandwidth (~2–3 MB once per slide), but requires installing GStreamer plugins on the Pi and exposes the 1 GB / 76 MB GPU X11 kiosk to WebKitGTK GStreamer buffer allocation spikes over multi-day uptime.
- **WebSocket binary JPEG frames rendered to `<canvas>`**: Avoids GStreamer and supports explicit ACK backpressure, but copies every 30 fps frame through JavaScript `ArrayBuffer` → `createImageBitmap` → 2D `<canvas>`, causing higher CPU and GC churn than WebKit's native C++ `libjpeg-turbo` `<img>` path.
- **Native `mpv` X11 window on the Pi**: Lowest raw decode CPU, but occludes MagicMirror's HTML clock, calendar, to-do, and weather overlays unless an X11 compositor (`picom`) is added, consuming scarce GPU memory.
- **Full 24/7 server-rendered video stream from Synology**: Minimizes Pi RAM (~40 MB), but burns Synology CPU continuously on static photos and softens crisp 2K widget typography via `4:2:0` chroma subsampling.

## Consequences

- Zero package changes or GStreamer dependencies are required on the Raspberry Pi 4 client; video frames render through the exact same leak-free C++ `<img>` path as still photos, with zero per-frame CSS `backdrop-filter` GPU load.
- Synology's RAM LRU cache keeps storing compact (~2 MB) MP4 buffers rather than bulky raw frame arrays, using ~2% NAS CPU during active video slides to pace the MJPEG loop.
- `photo-api` container `mem_limit` increases from `1300m` to `2048m` with `MAX_CONCURRENT_VIDEO_TRANSCODES=1` (`-threads 2`) so `ffmpeg` 4K HEVC decode memory stays safely outside Go's 1 GB cache heap.
