# Spec: Raspberry Pi 4 Low-Memory WebKitGTK Frame Client & Optional Server Pre-Baked Blur

## Problem Statement

On a 1 GB Raspberry Pi 4 Model B (Debian 12 Bookworm, Linux kernel 6.12, 948 MB ARM / 76 MB GPU memory split, running X11 on a 27-inch 2K 2560×1440 monitor mounted in portrait 1440×2560), running a full `MagicMirror` clone (`npm` + `node` + `Electron`) as the `Frame Client` consumes ~450–600 MB of RAM. Combined with Xorg and the desktop session, this pushes the 1 GB board into SD-card swap thrashing and makes slide transitions and UI rendering laggy. In addition, Debian 12 Bookworm's KMS display driver (`vc4-kms-v3d`) deprecates legacy `vcgencmd display_power` commands, and users who want zero client-side blur overhead need an optional server-side toggle to pre-composite blurred background bars into cached 2K `Preview`s on the NAS.

## Solution

Replace the heavy `npm` + `node` + `Electron` stack on the `Frame Client` with a lightweight native WebKitGTK kiosk viewer (`WebKit2-4.1` + `GTK3`, using ~100–140 MB RAM—a ~75% memory reduction) with automatic fallback to `chromium-browser` kiosk mode, eliminating the need to clone or install MagicMirror on the Raspberry Pi. Update the `Frame Client` display power and `Display Schedule` scripts to use X11 DPMS (`xset dpms force off/on`) on Debian 12 Bookworm with `vcgencmd` fallback. On the NAS (`photo-api`), preserve native 2K `Preview` resolution (`2560×2560` max box) and add an optional `PREBAKE_BLUR` feature toggle (defaulting to `false`) that, when enabled, composites a blurred, darkened `1440×2560` (configurable `FRAME_WIDTH×FRAME_HEIGHT`) background behind the aspect-fitted `Preview` before caching to disk.

## User Stories

### Lightweight WebKitGTK Frame Client
1. As a Raspberry Pi 4 owner with 1 GB RAM, I want the `Frame Client` to render the NAS MagicMirror URL in a minimal fullscreen WebKitGTK window instead of spawning `npm`, `node`, and `Electron`, so that browser RAM drops from ~500 MB to ~100–140 MB and swap lag disappears.
2. As a `Frame Client` operator, I want WebKitGTK configured with hardware acceleration (`HardwareAccelerationPolicy.ALWAYS`, accelerated 2D canvas, WebGL) and the low-memory `DOCUMENT_VIEWER` cache model, so that 2K `Preview`s render smoothly within the 76 MB GPU / 948 MB CPU memory split.
3. As a `Frame Client` operator, I want the WebKitGTK kiosk window to hide the mouse cursor, use a solid black background, and automatically retry loading the NAS URL every 5 seconds if the network or NAS restarts, so that the frame recovers hands-free without showing error dialogs.
4. As a `Frame Client` operator, I want the startup scripts to support a configurable `BROWSER_BACKEND` (`webkit` default, with automatic fallback to `chromium` kiosk mode with a 128 MB V8 heap cap), so that the client still runs even on systems where WebKitGTK is not installed.
5. As a `Frame Client` operator, I want to set up the Raspberry Pi 4 without cloning `~/MagicMirror` or installing gigabytes of Node/Electron dependencies on the SD card, so that installation is fast and lightweight.
6. As a `Frame Client` operator, I want the schedule manager and stop scripts to track and terminate the kiosk process cleanly by PID without leaving orphaned `npm` or browser processes behind, so that scheduled `Display Schedule` transitions never leak memory.

### Debian 12 Bookworm Display Power & X11 Hygiene
7. As a Raspberry Pi 4 operator on Debian 12 Bookworm (kernel 6.12 KMS), I want the PIR motion controller and schedule manager to power the monitor off and on using X11 DPMS (`xset -display :0 dpms force off/on`) with `vcgencmd display_power` as a fallback, so that motion-triggered display sleep and wake work reliably on modern Raspberry Pi OS.
8. As a `Frame Client` operator, I want Xorg's default 10-minute screen blanker disabled on kiosk startup (`xset s off -dpms s noblank`), so that X11 does not blank the display independently while the PIR sensor sees active motion.
9. As a `Frame Client` operator, I want the automated client installer to install `python3-gi`, `gir1.2-webkit2-4.1`, `gir1.2-gtk-3.0`, `x11-xserver-utils`, and `python3-gpiozero` via `apt`, so that a fresh Bookworm Pi 4 is ready in one command.

### Native 2K Sizing & Server-Side `PREBAKE_BLUR` Feature Toggle
10. As a viewer with a 27-inch 2K (2560×1440 / portrait 1440×2560) monitor, I want `photo-api` to keep its default `MAX_WIDTH=2560` and `MAX_HEIGHT=2560` bounding box, so that both portrait and landscape photos retain full native 2K sharpness.
11. As a NAS operator, I want a `PREBAKE_BLUR` feature toggle on `photo-api` that defaults to `false`, so that out of the box `photo-api` serves standard aspect-preserved 2K `Preview`s and WebKitGTK renders `MMM-ImmichSlideShow`'s native CSS blur.
12. As a NAS operator, I want setting `PREBAKE_BLUR=true` (with configurable `FRAME_WIDTH`, default `1440`, and `FRAME_HEIGHT`, default `2560`) to composite a blurred, darkened `cover` background behind the centered `contain` photo directly into the cached JPEG on the NAS, so that I can offload 100% of blur computation from the Pi 4 whenever desired.

## Implementation Decisions

- **WebKitGTK Kiosk Process**: A standalone Python 3 GObject Introspection script (`Gtk 3.0` + `WebKit2 4.1`) acts as the default `Frame Client` viewer. It runs as a single process directly managed by the schedule manager and manual start/stop scripts, with no `npm` or Node parent wrapper.
- **Browser Backend Selection**: `config.conf` exposes `BROWSER_BACKEND` (`webkit` | `chromium` | `electron`, defaulting to `webkit`). When `webkit` is selected, the runner checks that `WebKit2` is importable and falls back automatically to `chromium-browser` / `chromium` (`--kiosk --app=<url> --enable-gpu-rasterization --ignore-gpu-blocklist --js-flags="--max-old-space-size=128"`) if unavailable.
- **Bookworm KMS Display Power**: Display power helpers (in both the Python PIR daemon and Bash scripts) execute `xset -display "${DISPLAY:-:0}" dpms force on` / `off` when `xset` is available, and also invoke `vcgencmd display_power 1` / `0` as a fallback for older firmware. Display state detection parses `xset -display :0 q` (`Monitor is On` / `Monitor is Off`) before falling back to `vcgencmd`.
- **Optional Server Pre-Baked Blur (`PREBAKE_BLUR`)**:
  - Controlled via environment variables `PREBAKE_BLUR` (boolean, default `false`), `FRAME_WIDTH` (int, default `1440`), and `FRAME_HEIGHT` (int, default `2560`).
  - When `PREBAKE_BLUR=false` (default): `Preview` generation scales the EXIF-oriented image to fit within `MAX_WIDTH×MAX_HEIGHT` (`2560×2560`) without padding.
  - When `PREBAKE_BLUR=true`: `Preview` generation creates a `FRAME_WIDTH×FRAME_HEIGHT` RGBA canvas, fills it with a center-cropped (`cover`) version of the photo blurred via a fast downscale-box-blur-upscale pass and darkened by ~35% (for widget legibility), and composites the sharp aspect-fitted (`contain`) photo centered on top before encoding to JPEG and storing in the 1 GB LRU cache.

## Testing Decisions

- **Primary Seam (Existing Go HTTP + Real Temp `Library` & SQLite Seam)**:
  - Extend the existing `photo-api` end-to-end HTTP integration test suite to verify both `PrebakeBlur: false` (default: preserves original aspect ratio within `2560×2560`) and `PrebakeBlur: true` (renders an exact `1440×2560` portrait 2K JPEG composite with non-black blurred top/bottom bars around a landscape source image).
- **Secondary Seam (Client Script & Kiosk Verification)**:
  - Verify Python compilation (`python3 -m py_compile`) on the WebKitGTK kiosk script and PIR controller, and Bash syntax validation (`bash -n`) across all modified `Frame Client` scripts.

## Out of Scope

- Wayland / Labwc compositor migration on the Raspberry Pi 4 (the Pi 4 is running X11 `Xorg + LXDE/Openbox`, which WebKitGTK and `xset dpms` target directly).
- Removing `showBlurredImageForBlackBars` from `MMM-ImmichSlideShow` when `PREBAKE_BLUR=false`.

## Further Notes

- If the Pi 4 X11 session is currently outputting `1600x900` instead of the monitor's native `2560x1440` EDID mode, `xrandr --output HDMI-1 --mode 2560x1440 --rotate left` (or `right`) can be set in the X11 session or `/boot/firmware/cmdline.txt` (`video=HDMI-A-1:2560x1440M@60,rotate=90`).
