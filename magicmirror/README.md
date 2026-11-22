# magicmirror

Thin Dockerized **MagicMirror²** server extending `karsten13/magicmirror:latest` with pre-installed 3rd-party modules (zero git submodules or manual `npm install` on Synology NAS):

- `MMM-ImmichSlideShow` — connects privately to `http://photo-api:2283` over the Compose bridge network
- `MMM-Worldclock` — secondary timezone display (e.g. Vietnam time)
- `MMM-OpenWeatherMapForecast` — local weather forecast

## Setup

1. **Create your local `config.js`** from the secret-free template (gitignored so secrets never leak):
   ```bash
   cp magicmirror/config/config.js.example magicmirror/config/config.js
   ```
2. **Edit `magicmirror/config/config.js`**:
   - Replace `CHANGE_ME_TO_MATCH_API_KEY_IN_ENV` with the same `API_KEY` you set in the root `.env`.
   - Replace `YOUR_OPENWEATHERMAP_API_KEY` and coordinates with your own values.
   - Keep `url: "http://photo-api:2283"` — it talks directly to `photo-api` over the internal Docker network.
3. **Customize styling (optional)**: edit `magicmirror/css/custom.css`.
4. **Start the stack** from the repository root:
   ```bash
   docker compose up -d --build
   ```
   MagicMirror will be served at `http://<your-nas-ip>:8036` for the Raspberry Pi `Frame Client`.
