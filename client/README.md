# client (Raspberry Pi 3B Frame Client)

Scripts, unified systemd `Display Schedule`, and event-driven GPIO PIR motion sensor controller for running MagicMirror² in **client-only X11 mode** on a Raspberry Pi 3B driving a 27-inch 2K (2560×1440) monitor.

## Architecture
- Connects over LAN to your Synology NAS MagicMirror server (`http://<nas-ip>:8036`).
- Forces **X11 mode** for optimal Raspberry Pi 3B rendering performance.
- Runs a single unified systemd service (`digitalframe.service`) that polls the `Display Schedule` every 30 seconds and auto-restarts on crash.
- Uses an **event-driven PIR motion sensor on GPIO 24** (~0% idle CPU) with a 5-minute idle display power-off via `vcgencmd`.
- Logs exclusively to `systemd-journald` capped at 64MB / 3 days retention (no `/tmp` log buildup).

## Quick Setup on Raspberry Pi 3B

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
   Set your Synology NAS IP/port (`8036`), weekday/weekend `Display Schedule`, and PIR idle timeout.
3. **Run the automated installer**:
   ```bash
   ./setup_client.sh
   ```
4. **Verify service & logs**:
   ```bash
   sudo systemctl status digitalframe.service
   journalctl -u digitalframe.service -f
   ```

## PIR Sensor Wiring (HC-SR501)
- **VCC** → Pin 2 or 4 (`5V`)
- **GND** → Pin 6 (`GND`)
- **OUT** → Pin 18 (`GPIO 24`, configurable in `pir-control-display/`)

## Useful Commands
- Restart frame service: `sudo systemctl restart digitalframe.service`
- Check server connectivity: `./check_server.sh`
- View journald log guide: see [journald-config.md](./journald-config.md)
- Uninstall everything cleanly: `./uninstall.sh`
