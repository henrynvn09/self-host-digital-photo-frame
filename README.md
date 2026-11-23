# Self-Hosted Digital Photo Frame

A self-hosted 27-inch 2K family photo frame powered by a Synology NAS (`server/`) and a Raspberry Pi (`client/`). It replaces a 2 to 4 GB Immich stack with a ~20 MB pure-Go photo indexer, a Dockerized MagicMirror² display server, and a motion-aware WebKitGTK kiosk client.

## Features

- **Native 2K photo slideshow (`photo-api` + `MMM-ImmichSlideShow`)**: Reads your Synology Photos folder in read-only mode (`:ro`), converts JPEG, PNG, WebP, and iPhone `.heic` photos on demand into a 1 GB LRU cache, tags photos with offline city names, and alternates between Anniversary photos (within 45 days across past years) and Random photos.
- **Google Calendar appointments (`CUỘC HẸN SẮP TỚI`)**: Pulls upcoming family events every hour from a Google Calendar share link (`?cid=...`) or secret `.ics` link.
- **Google Sheets checklist (`Việc cần làm`)**: Pulls active to-do items every minute directly from a shared Google Spreadsheet (using [`todo-template.csv`](./server/magicmirror/config/todo-template.csv)) with no OAuth tokens required. Checking a box in Google Sheets hides the task on the frame within 60 seconds.
- **World clock and weather forecast**: Shows local date and time, secondary timezone (`VIETNAM`), and a multi-day OpenWeatherMap forecast at the bottom of the screen.
- **Schedule and motion power saving (`client/`)**: Runs the display only during configured weekday and weekend hours and powers the monitor off after 5 minutes without motion on the GPIO PIR sensor.
- **Live configuration reload**: Mounts `server/magicmirror/config/` as a watched folder so edits to `config.js` or `custom.css` refresh the display in about 1 second without restarting Docker. The Raspberry Pi also re-reads `client/config.conf` once every hour.

## Architecture

1. **`server/` (Synology NAS)**: A single `docker-compose.yml` running two containers on a private bridge network:
   - `photo-api` (internal port `2283`): Indexes `/photos:ro` into SQLite and serves 2K JPEG previews.
   - `magicmirror` (host port `8036`): Serves the MagicMirror² UI, top-bar widgets, weather, and photo slideshow.
2. **`client/` (Raspberry Pi)**: Connects to `http://<nas-ip>:8036` in a fullscreen WebKitGTK window (`kiosk.py`) managed by the schedule daemon (`frame.sh`) and PIR motion script (`pir.py`).

## Step 1. Set up the Synology NAS (`server/`)

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
   - Import [`server/magicmirror/config/todo-template.csv`](./server/magicmirror/config/todo-template.csv) into Google Sheets, insert checkboxes in Column A (**Insert > Checkbox**), share the sheet as **Anyone with the link (Viewer)**, and paste its URL into `spreadsheetUrl` under `MMM-GoogleSheetsToDo`.
   - Set your OpenWeatherMap `apikey`, `latitude`, and `longitude` under `MMM-OpenWeatherMapForecast`.
5. Build and start the containers:
   ```bash
   docker compose up -d --build
   ```
   Verify the display in any browser at `http://<nas-ip>:8036`.

## Step 2. Set up the Raspberry Pi (`client/`)

1. Copy the client config template and set `SERVER_IP` to your NAS IP address:
   ```bash
   cd client
   cp config.conf.sample config.conf
   nano config.conf
   ```
2. Run the installer to install WebKitGTK and enable the `digitalframe.service` systemd unit:
   ```bash
   ./install.sh
   ```

## Updating settings later

| What you want to change | File to edit | How it takes effect |
| :--- | :--- | :--- |
| Add or check off to-do items | Your Google Sheet | Updates on screen within 1 minute |
| Add or edit calendar events | Your Google Calendar | Updates on screen within 1 hour |
| Widget URLs, poll intervals, slideshow speed/mode, or weather location | `server/magicmirror/config/config.js` | Live-reloads connected displays automatically within 2 seconds |
| Font sizes, margins, or card opacity | `server/magicmirror/config/custom.css` | Live-reloads connected displays automatically within 2 seconds |
| Display active hours (`MONDAY_FRIDAY`, `SATURDAY_SUNDAY`) or PIR timeout (`PIR_TIMEOUT_MINUTES`) | `client/config.conf` | Reloads automatically on the Pi every hour (or immediately with `sudo systemctl restart digitalframe.service`) |
| NAS photo folder path or cache size | `server/.env` | Run `docker compose up -d` in `server/` |

## Folder guides

- [`server/README.md`](./server/README.md): NAS Docker Compose commands and troubleshooting.
- [`server/magicmirror/README.md`](./server/magicmirror/README.md): MagicMirror modules, widget options, and custom CSS.
- [`server/photo-api/README.md`](./server/photo-api/README.md): Go photo API environment variables, endpoints, and tests.
- [`client/README.md`](./client/README.md): Raspberry Pi commands, logs, and portrait rotation.
- [`PRD.md`](./PRD.md): Architecture details and performance design.
