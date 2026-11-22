# client (Raspberry Pi 4 Frame Client)

Lightweight WebKitGTK fullscreen kiosk viewer, unified systemd `Display Schedule`, and event-driven GPIO 24 PIR motion sensor controller for running the digital photo frame on a **1 GB Raspberry Pi 4 Model B** (Debian 12 Bookworm, Linux kernel 6.12, X11, driving a 27-inch 2K 2560×1440 monitor in portrait 1440×2560).

## Why WebKitGTK Instead of `npm` + `Electron`?
- **~75% lower RAM (~100–140 MB vs. ~450–600 MB)**: Eliminates cloning `~/MagicMirror` on the Pi and drops `npm`, `node`, and `Electron`, preventing SD-card swap lag inside the Pi 4's 948 MB ARM / 76 MB GPU memory split.
- **Hardware-accelerated WebKit2 (`kiosk.py`)**: Uses `WebKit2.HardwareAccelerationPolicy.ALWAYS`, accelerated 2D canvas, WebGL, and the low-memory `DOCUMENT_VIEWER` cache model, with 5-second automatic retry on disconnect.
- **Debian 12 Bookworm KMS Display Power**: Uses X11 DPMS (`xset -display :0 dpms force on/off` + `xset q` state detection) with `vcgencmd display_power` fallback and disables Xorg's default 10-minute blanker.

## Quick Setup on Raspberry Pi 4 (Bookworm)

1. **Clone this repo** onto the Pi:
   ```bash
   git clone https://github.com/henrynvn09/self-host-digital-photo-frame.git ~/self-host-digital-photo-frame
   cd ~/self-host-digital-photo-frame/client
   ```
2. **Copy and edit your client config**:
   ```bash
   cp config.conf.sample config.conf
   nano config.conf
   ```
   Set your Synology NAS IP/port (`8036`), `BROWSER_BACKEND=webkit` (default), weekday/weekend `Display Schedule`, and PIR idle timeout.
3. **Run the automated installer** (installs `python3-gi`, `gir1.2-webkit2-4.1`, `gir1.2-gtk-3.0`, `x11-xserver-utils`, and `python3-gpiozero`):
   ```bash
   ./setup_client.sh
   ```
4. **Verify service & logs**:
   ```bash
   sudo systemctl status digitalframe.service
   journalctl -u digitalframe.service -f
   ```

## Native 2K (2560×1440 / Portrait 1440×2560) X11 Note
If X11 defaults to `1600x900` instead of your monitor's top `/sys/class/drm/card1-HDMI-A-1/modes` mode (`2560x1440`), set native 2K in X11:
```bash
DISPLAY=:0 xrandr --output HDMI-1 --mode 2560x1440 --rotate left
```

## PIR Sensor Wiring (HC-SR501)
- **VCC** → Pin 2 or 4 (`5V`)
- **GND** → Pin 6 (`GND`)
- **OUT** → Pin 18 (`GPIO 24`)
