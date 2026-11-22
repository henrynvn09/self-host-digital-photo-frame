#!/bin/bash
# Digital Photo Frame client viewer startup (Raspberry Pi 4 Bookworm X11)
# Supports BROWSER_BACKEND=webkit (default, ~100-140MB RAM), chromium, or electron.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MM_DIR="${HOME}/MagicMirror"
CONFIG_FILE="${SCRIPT_DIR}/config.conf"

SERVER_IP="192.168.4.45"
SERVER_PORT="8036"
BROWSER_BACKEND="webkit"

if [[ -f "$CONFIG_FILE" ]]; then
	# shellcheck disable=SC1090
	source "$CONFIG_FILE" 2>/dev/null || true
fi

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

if [[ -f "${SCRIPT_DIR}/check_server.sh" ]]; then
	log "Checking server connectivity before starting..."
	if ! "${SCRIPT_DIR}/check_server.sh" 5 3; then
		log "ERROR: MagicMirror server at ${SERVER_IP}:${SERVER_PORT} is not reachable!"
		exit 1
	fi
fi

export DISPLAY="${DISPLAY:-:0}"
TARGET_URL="http://${SERVER_IP}:${SERVER_PORT}"

# Disable Xorg default 10-minute screen blanker so PIR/schedule controls sleep
if command -v xset >/dev/null 2>&1; then
	xset -display "$DISPLAY" s off -dpms s noblank 2>/dev/null || true
fi

can_use_webkit() {
	python3 -c 'import gi; gi.require_version("Gtk", "3.0"); gi.require_version("WebKit2", "4.1")' >/dev/null 2>&1 || \
	python3 -c 'import gi; gi.require_version("Gtk", "3.0"); gi.require_version("WebKit2", "4.0")' >/dev/null 2>&1
}

find_chromium() {
	for bin in chromium-browser chromium; do
		if command -v "$bin" >/dev/null 2>&1; then
			echo "$bin"
			return 0
		fi
	done
	return 1
}

if [[ "$BROWSER_BACKEND" == "webkit" ]]; then
	if can_use_webkit; then
		log "Starting WebKitGTK kiosk viewer connecting to ${TARGET_URL}..."
		exec python3 "${SCRIPT_DIR}/kiosk.py" "${TARGET_URL}"
	fi
	log "WebKitGTK not installed, falling back to Chromium kiosk..."
	BROWSER_BACKEND="chromium"
fi

if [[ "$BROWSER_BACKEND" == "chromium" ]]; then
	CHROMIUM_BIN="$(find_chromium || true)"
	if [[ -n "$CHROMIUM_BIN" ]]; then
		log "Starting ${CHROMIUM_BIN} kiosk connecting to ${TARGET_URL}..."
		exec "$CHROMIUM_BIN" \
			--kiosk \
			--app="${TARGET_URL}" \
			--noerrdialogs \
			--disable-infobars \
			--disable-session-crashed-bubble \
			--disable-features=TranslateUI \
			--enable-gpu-rasterization \
			--ignore-gpu-blocklist \
			--js-flags="--max-old-space-size=128"
	fi
	log "Chromium not found, falling back to legacy Electron..."
fi

# Legacy fallback: ~/MagicMirror Electron
export clientonly=1
export config="{\"address\":\"${SERVER_IP}\",\"port\":${SERVER_PORT}}"
cd "$MM_DIR" || exit 1
log "Starting legacy MagicMirror Electron client connecting to ${TARGET_URL}..."
exec npm run start:x11
