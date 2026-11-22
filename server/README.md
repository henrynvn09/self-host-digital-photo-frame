# server (Synology NAS)

Runs `photo-api` (pure-Go 2K photo indexer + 1 GB LRU cache, internal port `2283`) and `magicmirror` (port `8036`) on one private Docker network (~146 MiB total RAM).

## 3-Step Setup on Synology NAS

1. **Find your DSM user ID** via SSH (`id <username>`, usually `PUID=1026` and `PGID=100`) and create a writable data folder (for example `/volume1/docker/photoframe/data`).
2. **Create `.env` and `magicmirror/config/config.js`**:
   ```bash
   cd server
   cp .env.example .env && nano .env
   cp magicmirror/config/config.js.example magicmirror/config/config.js && nano magicmirror/config/config.js
   ```
   Set `PHOTOS_PATH` (your Synology Photos path, mounted read-only `:ro`), `DATA_PATH`, and a matching `API_KEY` in both files.
3. **Start the stack**:
   ```bash
   docker compose up -d --build
   ```

Open `http://<nas-ip>:8036` in any browser (or point `client/config.conf` on the Raspberry Pi 4 to `<nas-ip>`).

## Common Commands

```bash
docker compose ps                              # Check container health
docker compose logs -f photo-api               # Watch photo scan progress
docker compose exec photo-api curl -X POST -H "x-api-key: <API_KEY>" http://127.0.0.1:2283/api/admin/scan
docker compose down                            # Stop stack
```
