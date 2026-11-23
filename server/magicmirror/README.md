# magicmirror

Thin Dockerized **MagicMirror²** server extending pinned `karsten13/magicmirror:v2.38.0` with commit-pinned third-party modules and zero manual `npm install` steps on the Synology NAS:

- `MMM-ImmichSlideShow`: connects privately to `http://photo-api:2283` over the Compose bridge network.
- `MMM-Worldclock`: secondary timezone display (`VIETNAM`).
- `calendar` (built-in): top-center upcoming events (`CUỘC HẸN SẮP TỚI`) synced every 1 minute from a Google Calendar share link (`?cid=...`) or iCal `.ics` URL.
- `MMM-GoogleSheetsToDo` (bundled in `magicmirror/modules/MMM-GoogleSheetsToDo`): top-right to-do list (`Việc cần làm`) synced every 1 minute from a Google Sheet CSV export with zero OAuth tokens.
- `MMM-OpenWeatherMapForecast`: local weather forecast at `bottom_center`.

## Setup

From the `server/` directory:

1. **Create your local `config.js`** from the secret-free template (gitignored so secrets stay local):
   ```bash
   cp magicmirror/config/config.js.example magicmirror/config/config.js
   ```
2. **Edit `magicmirror/config/config.js`**:
   - Replace `CHANGE_ME_TO_MATCH_API_KEY_IN_ENV` with the same `API_KEY` you set in `server/.env`.
   - Replace `YOUR_OPENWEATHERMAP_API_KEY` and coordinates with your own values.
   - For **`calendar` (`CUỘC HẸN SẮP TỚI`)**: paste either your Google Calendar share link (`https://calendar.google.com/calendar/u/0?cid=...` for a public calendar) or the **Secret address in iCal format** (`.ics`) into `resolveGoogleCalendarUrl(...)` under `calendars[0].url` (`fetchInterval: 60 * 1000` refreshes every 1 minute).
   - For **`MMM-GoogleSheetsToDo` (`Việc cần làm`)**: import [`magicmirror/config/todo-template.csv`](./config/todo-template.csv) into Google Sheets (**File > Import > Upload**), select Column A (`Done`) and click **Insert > Checkbox**, share the sheet as **Anyone with the link (Viewer)**, and paste the Google Sheet URL into `spreadsheetUrl` (`refreshSeconds: 60` refreshes every 1 minute).
   - Keep `url: "http://photo-api:2283"`, which talks directly to `photo-api` over the internal Docker network.
3. **Customize styling (optional)**: edit `magicmirror/config/custom.css`. Because `magicmirror/config/` is mounted as a directory and MagicMirror runs `npm run server:watch` with `reloadAfterServerRestart: true`, edits to `magicmirror/config/config.js` and `magicmirror/config/custom.css` reload connected displays automatically within 2 seconds.
4. **Start the stack** from `server/`:
   ```bash
   docker compose up -d --build
   ```
   MagicMirror listens at `http://<your-nas-ip>:8036` for the Raspberry Pi `Frame Client`.
