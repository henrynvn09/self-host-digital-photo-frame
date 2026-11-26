# Self-Hosted Digital Photo Frame

A lightweight, self-hosted digital photo frame for a 27-inch 2K monitor powered by a Synology NAS (`server/`) and a Raspberry Pi (`client/`). It displays native 2K photos and silent videos from your NAS alongside Google Calendar, a Google Sheets to-do checklist, weather, world clock, and PIR motion sensor power control.

<img src="https://github.com/user-attachments/assets/06a367d3-47bb-4454-9f28-7fd8bd04826f" height="500">

## Features

### Photos & Videos
- 🖼️ **2K Photos + Silent Videos** — Displays your NAS photos (including iPhone `.heic`) and videos (`.mp4`, `.mov`, `.m4v`, `.webm`) with pre-blurred backgrounds, capture dates, album names, and offline city/country tags.
- 🎬 **Smart Video Playback** — Plays videos silently at up to 30 fps with a 30-second default cap (`shortVideoMode: "advance"` moves right to the next slide when shorter clips finish, or `"loop"` to fill the window). Automatically filters out short iPhone Live Photo clips.
- 🗓️ **Memories & Shuffle Without Repeats** — Surfaces "on this day" memories across past years (`anniversary`) and shuffles your full library (`random`) without repeating items back-to-back.
- 🔒 **Safe & Quiet on Your NAS** — Mounts your library strictly read-only (`:ro`) so original files are never touched, caches previews in 1 GB of RAM with zero disk writes, and prefetches the next slide in the background for instant transitions.

### Dashboard & Live Updates
- 📅 **Google Calendar** — Shows upcoming events (`CUỘC HẸN SẮP TỚI`) with `DD/MM` start–end dates and 24-hour times, synced every minute from a Google Calendar share link or `.ics` feed.
- ✅ **Google Sheets To-Do** — Syncs your checklist (`Việc cần làm`) every minute directly from a shared Google Spreadsheet ([`todo-template.csv`](./server/magicmirror/config/todo-template.csv)) with zero OAuth setup.
- 🌤️ **Weather & World Clock** — Multi-day OpenWeatherMap forecast plus a secondary timezone clock with flag.
- ⚡ **Instant Config Reload** — Edits to `config.js` or `custom.css` reload connected displays automatically within 2 seconds without restarting Docker; newly added NAS photos and videos appear automatically on background scans (every 30 minutes by default).

### Raspberry Pi Display
- 👋 **PIR Motion & Daily Schedule** — Automatically wakes and sleeps the monitor using a GPIO pin 24 motion sensor within separate weekday and weekend active hours.
- 🪶 **Lightweight & Self-Healing** — Runs smoothly on a 1 GB Raspberry Pi 4 in a minimal WebKitGTK kiosk (all video decoding happens on the NAS, so zero extra codecs are needed on the Pi) with automatic systemd recovery.

## Architecture

- **Server (Synology NAS)**: Single `server/docker-compose.yml` running `photo-api` (internal port `2283`) and `magicmirror` (port `8036`) on a private Docker network (~150 MB total RAM)
- **Client (Raspberry Pi 4)**: Lightweight fullscreen WebKitGTK kiosk (`client/kiosk.py`, ~120 MB RAM) connecting to `http://<nas-ip>:8036`
- **Display Control**: Event-driven PIR motion sensor on GPIO pin 24 (`client/pir.py`) with Bookworm X11 DPMS (`xset`) and `vcgencmd` fallback

---

## Step 1. Set Up the Synology NAS (`server/`)

You can set up everything directly in the **Synology DSM web interface** — no SSH or terminal required.

### Before You Start
Open **Package Center** in DSM and make sure **Container Manager** (DSM 7.2+) is installed.

### 1. Download & Edit `.env` on Your Computer
*(Synology File Station / Text Editor cannot save filenames starting with a dot like `.env`, so edit the config files on your computer first, then upload them to the NAS.)*

1. Download this repository as a ZIP (**Code > Download ZIP** on GitHub) and unzip it on your computer.
2. Open the `server/` folder, duplicate `.env.example`, and rename the copy to `.env`.
   - **macOS**: Press `Cmd + Shift + .` in Finder to show hidden files like `.env.example`.
   - **Windows**: Open `.env.example` in Notepad, choose **File > Save As**, set **Save as type** to **All Files (`*.*`)**, and save as `.env` (not `.env.txt`).
3. Edit `.env` and update these lines:
   - `PHOTOS_PATH` — path to your Synology Photos folder:
     - Shared Space: `/volume1/photo`
     - Personal Space: `/volume1/homes/<your-username>/Photos`
     *(Tip: in DSM **File Station**, right-click your photos folder → **Properties** → check **Location**.)*
   - `DATA_PATH` — set to `/volume1/docker/photoframe/data`.
   - `API_KEY` — change to any random secret string (you will paste this same value in step 2).
   - `TZ` — set to your timezone (for example `America/Los_Angeles`).
   - `PUID` / `PGID` — keep the default `1026` / `100` (works for most Synology admin accounts).
     <details>
     <summary>Need to check your exact PUID/PGID without SSH?</summary>

     1. Open **Control Panel > Task Scheduler > Create > Scheduled Task > User-defined script**.
     2. Under **General**, set **User** to the DSM account that has read access to your Photos folder.
     3. Under **Task Settings > Run command**, enter `id` and click **OK**.
     4. Select the new task, click **Run**, then click **Action > View Result** to see `uid=...` (`PUID`) and `gid=...` (`PGID`). You can delete the task afterward.
     </details>

### 2. Edit `config.js` on Your Computer
1. Open `server/magicmirror/config/`, duplicate `config.js.example`, and rename the copy to `config.js`.
2. Open `config.js` in any text editor and update these 4 sections:
   - **Photos (`MMM-MediaBackground`)**: Replace both `"CHANGE_ME_TO_MATCH_API_KEY_IN_ENV"` values with the exact `API_KEY` from your `.env` file.
   - **Google Calendar (`calendar`)**: Replace `YOUR_GOOGLE_CALENDAR_CID_OR_ICS_URL` with your Google Calendar share link (`https://calendar.google.com/calendar/u/0?cid=...`) or `.ics` link.
   - **Google Sheets To-Do (`MMM-GoogleSheetsToDo`)**: Import [`todo-template.csv`](./server/magicmirror/config/todo-template.csv) into Google Sheets, add checkboxes (**Insert > Checkbox** on Column A), click **Share > Anyone with the link (Viewer)**, and paste the link into `spreadsheetUrl`.
   - **Weather (`MMM-OpenWeatherMapForecast`)**: Fill in your OpenWeatherMap `apikey`, `latitude`, and `longitude`.

### 3. Upload `server/` to Your NAS in File Station
1. Open **File Station** in DSM and open the `docker` shared folder.
2. Click **Create > Create folder** and name it `photoframe` (creating `/docker/photoframe`).
3. Open `/docker/photoframe` and upload everything inside your edited `server/` folder (`docker-compose.yml`, `.env`, `photo-api/`, `magicmirror/`).
4. Inside `/docker/photoframe`, click **Create > Create folder** again and name it `data` (this stores the photo index).

### 4. Start the Stack in Container Manager
1. Open **Container Manager** from the DSM Main Menu.
2. In the left sidebar, click **Project**, then click **Create**.
3. Fill in the wizard:
   - **Project name**: `photoframe`
   - **Path**: Click **Set Path** and select `/docker/photoframe`
   - **Source**: Select **Use existing docker-compose.yml**
4. Click **Next**, skip Web Portal settings (**Next**), leave **Start the project once it is created** checked, and click **Done**.
5. Wait 2–5 minutes for the first build to finish. Once both `photo-api` and `magicmirror` show a green **Running** status, open `http://<nas-ip>:8036` in any browser to verify your frame.

<details>
<summary>Prefer SSH / Command Line?</summary>

1. SSH into your NAS and check your user/group IDs:
   ```bash
   id
   ```
2. Copy and edit the templates:
   ```bash
   cd server
   cp .env.example .env && nano .env
   cp magicmirror/config/config.js.example magicmirror/config/config.js && nano magicmirror/config/config.js
   ```
3. Build and start the containers:
   ```bash
   docker compose up -d --build
   ```
</details>

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
| Add, edit, or delete photos/videos | Your NAS `PHOTOS_PATH` folder | Indexed automatically every 30 minutes (`SCAN_INTERVAL`) without restarting |
| Add or check off to-do items | Your Google Sheet | Updates on screen within 1 minute |
| Add or edit calendar events | Your Google Calendar | Updates on screen within 1 minute |
| Widget URLs, poll intervals, slideshow speed/mode, or weather location | `server/magicmirror/config/config.js` | Live-reloads connected displays automatically within 2 seconds |
| Font sizes, margins, or card opacity | `server/magicmirror/config/custom.css` | Live-reloads connected displays automatically within 2 seconds |
| Display active hours (`MONDAY_FRIDAY`, `SATURDAY_SUNDAY`) or PIR timeout (`PIR_TIMEOUT_MINUTES`) | `client/config.conf` | Reloads automatically on the Pi every hour (or immediately with `sudo systemctl restart digitalframe.service`) |
| NAS photo folder path or cache size | `server/.env` | In **Container Manager > Project > photoframe**, click **Action > Build** (or run `docker compose up -d` in `server/`) |

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
- [MMM-ImmichSlideShow](https://github.com/pelaxa/MMM-ImmichSlideShow) - Inspiration for `MMM-MediaBackground` and anniversary slideshow workflows
- [MMM-Worldclock](https://github.com/ulrichwisser/MMM-Worldclock) - Secondary timezone display
- [MMM-OpenWeatherMapForecast](https://github.com/henrynvn09/MMM-OpenWeatherMapForecast) - Weather forecast
- Community modules and contributors

## Support

For issues or questions:

1. Check [AGENTS.md](./AGENTS.md) for repository overview and agent guidelines
2. Review the [Troubleshooting](./client/README.md#troubleshooting) section in `client/README.md` (PIR sensor, display power, server connectivity, and journald logs)
3. Check logs: `journalctl -fu digitalframe.service`
