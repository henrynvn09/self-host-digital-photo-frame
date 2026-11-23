# client (Raspberry Pi 4)

Minimal 4-file client for a **1 GB Raspberry Pi 4** (Debian 12 Bookworm, X11, 27-inch 2K monitor). Uses a native WebKitGTK fullscreen kiosk (~100 to 140 MB RAM) instead of `npm` + `Electron` (~500 MB RAM).

## Files

- `install.sh` — One-command installer (`./install.sh`) and uninstaller (`./install.sh --uninstall`).
- `frame.sh` — Unified controller (`./frame.sh daemon`, `./frame.sh browser`, `./frame.sh on`, `./frame.sh off`).
- `kiosk.py` — Fullscreen hardware-accelerated WebKitGTK viewer with hidden cursor and 5s auto-retry.
- `pir.py` — Event-driven GPIO 24 PIR motion sensor using Bookworm X11 DPMS (`xset dpms`) + `vcgencmd` fallback.
- `config.conf.sample` — Template for `config.conf` (`SERVER_IP`, schedule, PIR pin/timeout). `frame.sh` and `pir.py` re-read `config.conf` automatically once every hour, or immediately via `sudo systemctl restart digitalframe.service`.

## 3-Step Setup on Raspberry Pi 4

```bash
cd ~/self-host-digital-photo-frame/client
cp config.conf.sample config.conf && nano config.conf
./install.sh
```

## Everyday Commands

```bash
sudo systemctl status digitalframe.service     # Check service status
journalctl -u digitalframe.service -f          # Follow live logs
sudo systemctl restart digitalframe.service    # Apply config.conf edits
./frame.sh browser                             # Test viewer manually
./install.sh --uninstall                       # Remove systemd service
```

## Native 2K Portrait Resolution (`1440x2560`)
If X11 defaults to `1600x900`, switch HDMI-1 to native 2K portrait:
```bash
DISPLAY=:0 xrandr --output HDMI-1 --mode 2560x1440 --rotate left
```
