# client (Raspberry Pi 4)

Minimal 4-file client for a **1 GB Raspberry Pi 4** (Debian 12 Bookworm, X11, 27-inch 2K monitor). Uses a native WebKitGTK fullscreen kiosk (~100 to 140 MB RAM) instead of `npm` and `Electron` (~500 MB RAM).

## Files

- `install.sh`: One-command installer (`./install.sh`) and uninstaller (`./install.sh --uninstall`).
- `frame.sh`: Unified controller (`./frame.sh daemon`, `./frame.sh browser`, `./frame.sh pir-test`, `./frame.sh check`, `./frame.sh on`, `./frame.sh off`).
- `kiosk.py`: Fullscreen hardware-accelerated WebKitGTK viewer with hidden cursor and 5s auto-retry.
- `pir.py`: Event-driven GPIO 24 PIR motion sensor using Bookworm X11 DPMS (`xset dpms`) and `vcgencmd` fallback.
- `config.conf.sample`: Template for `config.conf` (`SERVER_IP`, `SERVER_PORT`, `MONDAY_FRIDAY`, `SATURDAY_SUNDAY`, `PIR_GPIO_PIN`, `PIR_TIMEOUT_MINUTES`, `DEBUG`). `frame.sh` and `pir.py` re-read `config.conf` automatically once every hour, or immediately via `sudo systemctl restart digitalframe.service`.

## 3-Step Setup on Raspberry Pi 4

```bash
cd ~/self-host-digital-photo-frame/client
cp config.conf.sample config.conf && nano config.conf
./install.sh
```

## PIR Sensor Hardware & Configuration

- **VCC**: 5V (Physical Pin 2 or 4)
- **GND**: Ground (Physical Pin 6)
- **OUT**: **BCM GPIO 24** (Physical Pin 18)

In `client/config.conf`:

```ini
PIR_GPIO_PIN=24
PIR_TIMEOUT_MINUTES=5
```

## Everyday Commands

```bash
sudo systemctl status digitalframe.service     # Check service status
journalctl -fu digitalframe.service            # Follow live logs
journalctl -u digitalframe.service -n 50       # View last 50 log lines
sudo systemctl restart digitalframe.service    # Apply config.conf edits immediately
./frame.sh check                               # Test connectivity to NAS MagicMirror server
./frame.sh pir-test                            # Test PIR motion sensor & DPMS live in terminal
./frame.sh browser                             # Test WebKitGTK viewer manually
./frame.sh on                                  # Force monitor display power ON
./frame.sh off                                 # Stop viewer and force monitor display power OFF
./install.sh --uninstall                       # Remove systemd service
```

## Troubleshooting

### 1. PIR Sensor Not Working (Display Doesn't Turn On/Off with Motion)

1. **Check service and PIR process status**:
   ```bash
   systemctl status digitalframe.service
   ps aux | grep pir.py
   ```
2. **Test the PIR sensor interactively**:
   ```bash
   ./frame.sh pir-test
   ```
   Wave your hand in front of the sensor. You will see:
   - `[PIR] Started on GPIO 24 (timeout=300s, display_off=False, debug=True)`
   - `[PIR] Motion detected`
   - `[PIR] Motion stopped -> scheduling display OFF in 300s`
3. **Check GPIO permissions and Bookworm `lgpio` package**:
   ```bash
   groups $(whoami)                                           # Must include 'gpio'
   sudo usermod -a -G gpio $(whoami)                          # Add user if missing (then logout/login)
   sudo apt-get install -y python3-gpiozero python3-lgpio
   ```
4. **Verify display power control (`xset` and `vcgencmd`)**:
   ```bash
   ./frame.sh off
   ./frame.sh on
   DISPLAY=:0 xset q | grep "Monitor is"
   vcgencmd display_power                                     # Legacy fallback check
   ```

### 2. Black Screen or Viewer Won't Start

1. **Test NAS server connectivity**:
   ```bash
   ./frame.sh check
   ```
   If unreachable, verify `SERVER_IP` and `SERVER_PORT` (`8036`) in `client/config.conf` and check that the NAS `magicmirror` container is running (`docker compose ps`).
2. **Check error logs**:
   ```bash
   journalctl -xeu digitalframe.service -n 100
   journalctl -u digitalframe.service -p err
   ```
3. **Restart clean**:
   ```bash
   ./frame.sh off
   sudo systemctl restart digitalframe.service
   ```

### 3. Debug Mode (`DEBUG=true`)

> **Warning:** Setting `DEBUG=true` logs every 30-second schedule check and every PIR motion event (~15 to 30 MB/day). Because `journald` is capped at 64 MB, enable `DEBUG=true` only while troubleshooting and set it back to `DEBUG=false` afterward.

```bash
nano ~/self-host-digital-photo-frame/client/config.conf   # Set DEBUG=true
sudo systemctl restart digitalframe.service
journalctl -fu digitalframe.service
```

When finished:

```bash
nano ~/self-host-digital-photo-frame/client/config.conf   # Set DEBUG=false
sudo systemctl restart digitalframe.service
sudo journalctl --vacuum-time=1h                          # Optional: clear debug logs
```

### 4. Journald Log Management (64 MB Cap)

`install.sh` configures `/etc/systemd/journald.conf.d/digitalframe.conf` with `SystemMaxUse=64M`, `SystemMaxFileSize=8M`, and `MaxRetentionSec=3day`.

```bash
journalctl --disk-usage                        # Check total log disk usage
journalctl --verify                            # Verify journal integrity
sudo journalctl --vacuum-time=3d               # Remove logs older than 3 days
sudo journalctl --vacuum-size=8M               # Trim journal size manually
```

## Native 2K Portrait Resolution (`1440x2560`)

If X11 defaults to `1600x900`, switch HDMI-1 to native 2K portrait:

```bash
DISPLAY=:0 xrandr --output HDMI-1 --mode 2560x1440 --rotate left
```
