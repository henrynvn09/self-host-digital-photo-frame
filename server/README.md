# server (Synology NAS)

Runs `photo-api` (pure-Go 2K photo indexer and 1 GB LRU cache on internal port `2283`) and `magicmirror` (port `8036`) on one private Docker network (~146 MiB total RAM).

## Setup via Synology DSM UI (No SSH Needed)

Make sure **Container Manager** (DSM 7.2+) is installed from **Package Center**.

1. **Prepare `.env` and `config.js` on your computer** *(DSM Text Editor cannot save filenames starting with `.` like `.env`)*:
   - Duplicate `server/.env.example` → `server/.env` (macOS: press `Cmd + Shift + .` to show dotfiles; Windows Notepad: set **Save as type** to **All Files** so it saves as `.env`).
     - Set `PHOTOS_PATH` (`/volume1/photo` for Shared Space or `/volume1/homes/<username>/Photos` for Personal Space), `DATA_PATH` (`/volume1/docker/photoframe/data`), `TZ`, and a secret `API_KEY`.
     - Keep `PUID=1026` and `PGID=100` (default for most Synology admin accounts; verify without SSH via **Control Panel > Task Scheduler > User-defined script** running `id`).
   - Duplicate `server/magicmirror/config/config.js.example` → `server/magicmirror/config/config.js`.
     - Set both `apiKey` values under `MMM-MediaBackground` to match `API_KEY` in `.env`, and fill in your Google Calendar, Google Sheets, and OpenWeatherMap details.
2. **Upload files in File Station**:
   - Create `/docker/photoframe` and upload everything from your edited `server/` folder (including `.env` and `config.js`) into it.
   - Create an empty `/docker/photoframe/data` folder for the SQLite index.
3. **Create & start the project in Container Manager**:
   - Open **Container Manager > Project > Create**.
   - Set **Project name** to `photoframe`, **Path** to `/docker/photoframe`, and **Source** to **Use existing docker-compose.yml**.
   - Finish the wizard with **Start the project once it is created** checked.

Open `http://<nas-ip>:8036` in any browser (or point `client/config.conf` on the Raspberry Pi to `<nas-ip>`). `magicmirror/config/` is mounted as a directory and watched with `server:watch`, so later edits to `config.js` or `custom.css` reload automatically on connected displays without restarting the container.

## Day-to-day Management in Container Manager UI

- **View logs**: **Container Manager > Container** → click `photo-api` or `magicmirror` → **Log** tab.
- **Apply `.env` changes**: **Container Manager > Project** → select `photoframe` → **Action > Build**.
- **Stop / Start**: **Container Manager > Project** → select `photoframe` → **Action > Stop** / **Start**.
- **One-click Git Update from DSM Web UI** (if you cloned the repo into `/volume1/docker/photoframe/self-host-digital-photo-frame`):
  1. Open **Control Panel > Task Scheduler > Create > Scheduled Task > User-defined script**.
  2. **General**: Task `Update Photoframe`, User **`root`**, uncheck **Enabled** (so it only runs when you click **Run**).
  3. **Task Settings > Run command**: `bash /volume1/docker/photoframe/update-nas.sh` → click **OK**.
  4. Anytime you want to pull the latest code and rebuild without SSH, select `Update Photoframe` and click **Run** (check progress via **Action > View Result**).

<details>
<summary>CLI / SSH Setup & Common Commands</summary>

```bash
cd server
cp .env.example .env && nano .env
cp magicmirror/config/config.js.example magicmirror/config/config.js && nano magicmirror/config/config.js
docker compose up -d --build
```

```bash
docker compose ps                              # Check container health
docker compose logs -f photo-api               # Watch photo scan progress
docker compose logs -f magicmirror             # Watch MagicMirror and live-reload logs
docker compose exec photo-api curl -X POST -H "x-api-key: <API_KEY>" http://127.0.0.1:2283/api/admin/scan
docker compose down                            # Stop stack
```
</details>
