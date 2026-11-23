# magicmirror

Thin Dockerized **MagicMirror²** server extending pinned `karsten13/magicmirror:v2.38.0` with commit-pinned 3rd-party modules (zero git submodules or manual `npm install` on Synology NAS):

- `MMM-ImmichSlideShow` — connects privately to `http://photo-api:2283` over the Compose bridge network
- `MMM-Worldclock` — secondary timezone display (e.g. Vietnam time)
- `calendar` (built-in) — top-center upcoming events ("CUỘC HẸN SẮP TỚI") synced every 1 hour from a Google Calendar share link (`?cid=...`) or iCal `.ics` URL
- `MMM-GoogleSheetsToDo` (bundled in `magicmirror/modules/MMM-GoogleSheetsToDo`) — top-right to-do list ("Việc cần làm") synced every 1 minute directly from a Google Sheet CSV export (zero OAuth tokens required)
- `MMM-OpenWeatherMapForecast` — local weather forecast

## Setup

From the `server/` directory:

1. **Create your local `config.js`** from the secret-free template (gitignored so secrets never leak):
   ```bash
   cp magicmirror/config/config.js.example magicmirror/config/config.js
   ```
2. **Edit `magicmirror/config/config.js`**:
   - Replace `CHANGE_ME_TO_MATCH_API_KEY_IN_ENV` with the same `API_KEY` you set in `server/.env`.
   - Replace `YOUR_OPENWEATHERMAP_API_KEY` and coordinates with your own values.
   - For **`calendar` ("CUỘC HẸN SẮP TỚI")**: paste either your Google Calendar share link (`https://calendar.google.com/calendar/u/0?cid=...` for a public calendar) or the **Secret address in iCal format** (`.ics`) into `resolveGoogleCalendarUrl(...)` under `calendars[0].url` (`fetchInterval: 60 * 60 * 1000` refreshes every 1 hour).
   - For **`MMM-GoogleSheetsToDo` ("Việc cần làm")**: import [`magicmirror/config/todo-template.csv`](config/todo-template.csv) into Google Sheets (*File → Import → Upload*), select Column A (`Done`) and click *Insert → Checkbox*, share the sheet as **Anyone with the link → Viewer**, and paste the Google Sheet URL into `spreadsheetUrl`. Supports 1-column task lists (`Mua sữa`, `Mua trứng`, `Nộp tiền bh`) or 2-column checkbox + task sheets (`[Checkbox, Task]` or `[Task, Checkbox]`), refreshing every `refreshSeconds: 60`.
   - Keep `url: "http://photo-api:2283"` — it talks directly to `photo-api` over the internal Docker network.
3. **Customize styling (optional)**: edit `magicmirror/css/custom.css`.
4. **Start the stack** from `server/`:
   ```bash
   docker compose up -d --build
   ```
   MagicMirror will be served at `http://<your-nas-ip>:8036` for the Raspberry Pi `Frame Client`.
