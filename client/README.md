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
sudo systemctl restart digitalframe.service    # Apply config.conf edits immediately
./frame.sh browser                             # Test WebKitGTK viewer manually
./frame.sh pir-test                            # Test PIR motion sensor & DPMS live in terminal
./frame.sh on                                  # Force monitor display power ON
./frame.sh off                                 # Stop viewer and force monitor display power OFF
./install.sh --uninstall                       # Remove systemd service
```

## Troubleshooting PIR Motion Sensor & Display Power

1. **Test the PIR sensor live in the terminal**:
   ```bash
   ./frame.sh pir-test
   ```
   Wave your hand in front of the sensor. You should see `[PIR] Listening on GPIO 24`, `[PIR] Motion detected`, and `[PIR] Motion stopped -> scheduling display OFF in 300s`.
2. **If `pir-test` fails to open GPIO 24**:
   - Check wiring: VCC to 5V (Pin 2 or 4), GND to GND (Pin 6), OUT to **BCM GPIO 24** (Physical Pin 18). If wired to another pin, update `PIR_GPIO_PIN` in `client/config.conf`.
   - Ensure the Bookworm GPIO backend and permissions are installed:
     ```bash
     sudo apt-get install -y python3-gpiozero python3-lgpio
     sudo usermod -a -G gpio "$USER"
     ```
     Log out and back in if group membership was just added.
3. **If motion is detected in `pir-test` but the monitor does not turn off or on**:
   - Test display power directly:
     ```bash
     ./frame.sh off
     ./frame.sh on
     DISPLAY=:0 xset q | grep "Monitor is"
     ```
   - Ensure the X11 server is running on `DISPLAY=:0` and that your monitor supports HDMI DPMS standby.
4. **Enable verbose PIR logs in systemd (`journalctl`)**:
   - Set `DEBUG=true` in `client/config.conf` and run `sudo systemctl restart digitalframe.service`, then follow logs with `journalctl -u digitalframe.service -f`. Set `DEBUG=false` when finished.

## Native 2K Portrait Resolution (`1440x2560`)
If X11 defaults to `1600x900`, switch HDMI-1 to native 2K portrait:
```bash
DISPLAY=:0 xrandr --output HDMI-1 --mode 2560x1440 --rotate left
```
