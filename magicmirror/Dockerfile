# Thin MagicMirror image extending karsten13/magicmirror with pinned modules
# (no git submodules or manual npm install required on Synology NAS)
FROM karsten13/magicmirror:latest

USER root
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*

USER node
WORKDIR /opt/magic_mirror/modules

RUN git clone --depth 1 https://github.com/pelaxa/MMM-ImmichSlideShow.git \
    && cd MMM-ImmichSlideShow && npm install --omit=dev

RUN git clone --depth 1 https://github.com/bkeyport/MMM-Worldclock.git \
    && cd MMM-Worldclock && npm install --omit=dev

RUN git clone --depth 1 https://github.com/jclarke0000/MMM-OpenWeatherMapForecast.git \
    && cd MMM-OpenWeatherMapForecast && npm install --omit=dev

WORKDIR /opt/magic_mirror
