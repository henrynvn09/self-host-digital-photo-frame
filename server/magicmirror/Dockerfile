# Thin MagicMirror image extending pinned karsten13/magicmirror with commit-pinned
# 3rd-party modules (no git submodules or manual npm install required on Synology NAS)
FROM karsten13/magicmirror:v2.38.0

USER root
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/magic_mirror/config && chown -R node:node /opt/magic_mirror/config

USER node
WORKDIR /opt/magic_mirror/modules

ARG IMMICH_SLIDESHOW_SHA=955c2538ccdd5b9702119826edcda549db937f55
ARG WORLDCLOCK_SHA=e6bfe734462fa13472e8728434294f4ec1a6c304
ARG OPENWEATHER_SHA=e36d50f725695c3d8649a35acbaeeb1446c33bbc

RUN git init MMM-ImmichSlideShow \
    && cd MMM-ImmichSlideShow \
    && git remote add origin https://github.com/pelaxa/MMM-ImmichSlideShow.git \
    && git fetch --depth 1 origin ${IMMICH_SLIDESHOW_SHA} \
    && git checkout --detach FETCH_HEAD \
    && npm install --omit=dev

RUN git init MMM-Worldclock \
    && cd MMM-Worldclock \
    && git remote add origin https://github.com/ulrichwisser/MMM-Worldclock.git \
    && git fetch --depth 1 origin ${WORLDCLOCK_SHA} \
    && git checkout --detach FETCH_HEAD \
    && npm install --omit=dev

RUN git init MMM-OpenWeatherMapForecast \
    && cd MMM-OpenWeatherMapForecast \
    && git remote add origin https://github.com/henrynvn09/MMM-OpenWeatherMapForecast.git \
    && git fetch --depth 1 origin ${OPENWEATHER_SHA} \
    && git checkout --detach FETCH_HEAD \
    && npm install --omit=dev

COPY --chown=node:node modules/MMM-GoogleSheetsToDo /opt/magic_mirror/modules/MMM-GoogleSheetsToDo

WORKDIR /opt/magic_mirror

RUN node -e ' \
  const fs = require("node:fs"); \
  let src = fs.readFileSync("serveronly/watcher.js", "utf8"); \
  src = src.replace( \
    "Log.log(`Watching file: ${file}`);", \
    "fs.watchFile(file, { interval: 2000 }, (curr, prev) => { if (curr.mtimeMs !== prev.mtimeMs || curr.size !== prev.size) { try { const cp = getConfigFilePath(); delete require.cache[require.resolve(cp)]; require(cp); } catch (e) { Log.error(`[watchFile] Ignoring invalid config.js save: ${e.message}`); return; } restartServer(`File changed: ${path.basename(file)} — restarting...`); } });\n\t\tLog.log(`Watching file: ${file}`);" \
  ); \
  fs.writeFileSync("serveronly/watcher.js", src); \
'

# Run MagicMirror in headless server-only watch mode (clients connect on port 8080/8036)
CMD ["npm", "run", "server:watch"]
