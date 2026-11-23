# Self-Hosted Digital Photo Frame

A lightweight, self-hosted digital photo frame for a 27-inch 2K monitor powered by a Synology NAS (`server/`) and a Raspberry Pi (`client/`). It syncs Google Calendar, a Google Sheets to-do checklist, weather, world clock, and displays native 2K photos from your NAS with PIR motion sensor power control.

<img src="https://github.com/user-attachments/assets/06a367d3-47bb-4454-9f28-7fd8bd04826f" height="500">

## Features

- 🖼️ **2K Photo Slideshow** - Lightweight pure-Go `photo-api` (~20 MB RAM) reads your Synology Photos folder read-only (`:ro`), caches 2K JPEG previews (including `.heic`) in a 1 GB LRU cache, and tags photos with offline city names
- 📅 **Google Calendar** - Shows upcoming events in Vietnamese (`CUỘC HẸN SẮP TỚI`) with `DD/MM` start-end dates and 24-hour times, synced every minute from a Google Calendar share link or `.ics` feed
- ✅ **Google Sheets To-Do** - Displays your checklist (`Việc cần làm`) synced every minute directly from a shared Google Spreadsheet ([`todo-template.csv`](./server/magicmirror/config/todo-template.csv)) with zero OAuth tokens
- 🌤️ **Weather Forecast** - Multi-day OpenWeatherMap integration
- 🌍 **World Clock** - Shows Vietnam time with flag
- ⚡ **Live Config Reload** - Edits to `server/magicmirror/config/config.js` or `custom.css` reload connected displays automatically within 2 seconds without restarting Docker
- 👋 **PIR Motion Sensor** - Automatically turns the display on/off based on motion on GPIO pin 24
- ⏰ **Auto Scheduling** - Schedule-aware systemd service with separate weekday/weekend hours and hourly live config reload
- 🔄 **Auto-Restart** - Automatically recovers from crashes via systemd and WebKitGTK auto-retry

## Architecture

- **Server (Synology NAS)**: Single `server/docker-compose.yml` running `photo-api` (internal port `2283`) and `magicmirror` (port `8036`) on a private Docker network (~150 MB total RAM)
- **Client (Raspberry Pi 4)**: Lightweight fullscreen WebKitGTK kiosk (`client/kiosk.py`, ~120 MB RAM) connecting to `http://<nas-ip>:8036`
- **Display Control**: Event-driven PIR motion sensor on GPIO pin 24 (`client/pir.py`) with Bookworm X11 DPMS (`xset`) and `vcgencmd` fallback

---

## Step 1. Set Up the Synology NAS (`server/`)

1. SSH into your NAS and find your user and group IDs:
   ```bash
   id
   ```
2. Copy the environment and MagicMirror config templates:
   ```bash
   cd server
   cp .env.example .env
   cp magicmirror/config/config.js.example magicmirror/config/config.js
   ```
3. Edit `server/.env`:
   - Set `PUID` and `PGID` from step 1.
   - Set `PHOTOS_PATH` to your Synology Photos folder (for example `/volume1/photo`).
   - Set `DATA_PATH` to a writable folder for the SQLite index and 1 GB preview cache.
   - Set `API_KEY` to any secret string.
4. Edit `server/magicmirror/config/config.js`:
   - Set `apiKey` in both `MMM-ImmichSlideShow` blocks to match `API_KEY` from `server/.env`.
   - Paste your Google Calendar share link (`https://calendar.google.com/calendar/u/0?cid=...`) or `.ics` link into `resolveGoogleCalendarUrl(...)` under `calendar`.
   - Import [`server/magicmirror/config/todo-template.csv`](./server/magicmirror/config/todo-template.csv) into Google Sheets (**Insert > Checkbox** on Column A), share as **Anyone with the link (Viewer)**, and paste its URL into `spreadsheetUrl` under `MMM-GoogleSheetsToDo`.
   - Set your OpenWeatherMap `apikey`, `latitude`, and `longitude` under `MMM-OpenWeatherMapForecast`.
5. Build and start the containers:
   ```bash
   docker compose up -d --build
   ```
   Verify the display in any browser at `http://<nas-ip>:8036`.

## Step 2. Set Up the Raspberry Pi (`client/`)

1. Copy the client config template and set `SERVER_IP` to your NAS IP address:
   ```bash
   cd client
   cp config.conf.sample config.conf
   nano config.conf
   ```
2. Run the installer to install WebKitGTK, GPIO packages (`python3-gpiozero`, `python3-lgpio`), and enable `digitalframe.service`:
   ```bash
   ./install.sh
   ```

## Updating Settings Later

| What you want to change | File to edit | How it takes effect |
| :--- | :--- | :--- |
| Add or check off to-do items | Your Google Sheet | Updates on screen within 1 minute |
| Add or edit calendar events | Your Google Calendar | Updates on screen within 1 minute |
| Widget URLs, poll intervals, slideshow speed/mode, or weather location | `server/magicmirror/config/config.js` | Live-reloads connected displays automatically within 2 seconds |
| Font sizes, margins, or card opacity | `server/magicmirror/config/custom.css` | Live-reloads connected displays automatically within 2 seconds |
| Display active hours (`MONDAY_FRIDAY`, `SATURDAY_SUNDAY`) or PIR timeout (`PIR_TIMEOUT_MINUTES`) | `client/config.conf` | Reloads automatically on the Pi every hour (or immediately with `sudo systemctl restart digitalframe.service`) |
| NAS photo folder path or cache size | `server/.env` | Run `docker compose up -d` in `server/` |

## Folder Guides

- [`server/README.md`](./server/README.md) - NAS Docker Compose commands and stack overview
- [`server/magicmirror/README.md`](./server/magicmirror/README.md) - MagicMirror modules, widget options, and custom CSS
- [`server/photo-api/README.md`](./server/photo-api/README.md) - Go photo API environment variables, endpoints, and tests
- [`client/README.md`](./client/README.md) - Raspberry Pi commands, PIR sensor wiring, and troubleshooting
- [`PRD.md`](./PRD.md) - Architecture details and performance design

---

## Contributing

This is a personal project, but feel free to contribute!

## License

MIT License - See repository for details

## Credits

- [MagicMirror²](https://magicmirror.builders/) - Open source smart mirror platform
- [MMM-ImmichSlideShow](https://github.com/pelaxa/MMM-ImmichSlideShow) - Photo slideshow integration
- [MMM-Worldclock](https://github.com/ulrichwisser/MMM-Worldclock) - Secondary timezone display
- [MMM-OpenWeatherMapForecast](https://github.com/henrynvn09/MMM-OpenWeatherMapForecast) - Weather forecast
- Community modules and contributors

## Support

For issues or questions:

1. Check [AGENTS.md](./AGENTS.md) for repository overview and agent guidelines
2. Review the [Troubleshooting](./client/README.md#troubleshooting) section in `client/README.md` (PIR sensor, display power, server connectivity, and journald logs)
3. Check logs: `journalctl -fu digitalframe.service`
