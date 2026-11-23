#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${SCRIPT_DIR}/config.conf"

SERVER_IP="192.168.4.45"
SERVER_PORT="8036"
MONDAY_FRIDAY="16:00-20:45"
SATURDAY_SUNDAY="08:00-20:45"
TARGET_URL=""

load_config() {
	if [[ -f "$CONFIG_FILE" ]]; then
		# shellcheck disable=SC1090
		source "$CONFIG_FILE"
	fi
	TARGET_URL="http://${SERVER_IP}:${SERVER_PORT}"
}

load_config
export DISPLAY="${DISPLAY:-:0}"
KIOSK_PID=""
PIR_PID=""

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

display_power() {
	local mode="$1"
	if command -v xset >/dev/null 2>&1; then
		if [[ "$mode" == "on" ]]; then
			xset -display "$DISPLAY" dpms force on 2>/dev/null || true
			xset -display "$DISPLAY" s off -dpms s noblank 2>/dev/null || true
		else
			xset -display "$DISPLAY" dpms force off 2>/dev/null || true
		fi
	fi
	if command -v /usr/bin/vcgencmd >/dev/null 2>&1; then
		if [[ "$mode" == "on" ]]; then
			/usr/bin/vcgencmd display_power 1 >/dev/null 2>&1 || true
		else
			/usr/bin/vcgencmd display_power 0 >/dev/null 2>&1 || true
		fi
	fi
}

launch_browser() {
	if ! python3 -c 'import gi; gi.require_version("Gtk", "3.0"); gi.require_version("WebKit2", "4.1")' 2>/dev/null && \
	   ! python3 -c 'import gi; gi.require_version("Gtk", "3.0"); gi.require_version("WebKit2", "4.0")' 2>/dev/null; then
		log "ERROR: WebKitGTK (gir1.2-webkit2-4.1) is not installed or failed to import. Run ./install.sh first."
		exit 1
	fi
	log "Starting WebKitGTK kiosk -> ${TARGET_URL}"
	exec python3 "${SCRIPT_DIR}/kiosk.py" "${TARGET_URL}"
}

in_schedule() {
	local range="$MONDAY_FRIDAY"
	[[ $(date +%u) -ge 6 ]] && range="$SATURDAY_SUNDAY"
	local on_str="${range%%-*}" off_str="${range##*-}"
	local now_min=$((10#$(date +%H) * 60 + 10#$(date +%M)))
	local on_min=$((10#${on_str%%:*} * 60 + 10#${on_str##*:}))
	local off_min=$((10#${off_str%%:*} * 60 + 10#${off_str##*:}))
	[[ $now_min -ge $on_min && $now_min -lt $off_min ]]
}

start_children() {
	display_power on
	"$0" browser &
	KIOSK_PID=$!
	python3 "${SCRIPT_DIR}/pir.py" &
	PIR_PID=$!
	log "Frame ON (kiosk PID=${KIOSK_PID}, pir PID=${PIR_PID})"
}

stop_children() {
	[[ -n "$PIR_PID" ]] && kill "$PIR_PID" 2>/dev/null || true
	[[ -n "$KIOSK_PID" ]] && kill "$KIOSK_PID" 2>/dev/null || true
	pkill -f "${SCRIPT_DIR}/kiosk.py" 2>/dev/null || true
	pkill -f "${SCRIPT_DIR}/pir.py" 2>/dev/null || true
	KIOSK_PID=""
	PIR_PID=""
	display_power off
	log "Frame OFF"
}

run_daemon() {
	trap 'stop_children; exit 0' INT TERM
	local active=false
	local last_reload=$SECONDS
	log "Schedule daemon started (Mon-Fri ${MONDAY_FRIDAY}, Sat-Sun ${SATURDAY_SUNDAY}, server ${TARGET_URL})"
	display_power off
	while true; do
		if (( SECONDS - last_reload >= 3600 )); then
			local prev_url="$TARGET_URL"
			load_config
			last_reload=$SECONDS
			if [[ "$active" == "true" && "$TARGET_URL" != "$prev_url" ]]; then
				[[ -n "$KIOSK_PID" ]] && kill "$KIOSK_PID" 2>/dev/null || true
				pkill -f "${SCRIPT_DIR}/kiosk.py" 2>/dev/null || true
			fi
		fi
		if in_schedule; then
			if [[ "$active" == "false" ]]; then
				start_children
				active=true
			else
				[[ -n "$KIOSK_PID" ]] && ! kill -0 "$KIOSK_PID" 2>/dev/null && { "$0" browser & KIOSK_PID=$!; }
				[[ -n "$PIR_PID" ]] && ! kill -0 "$PIR_PID" 2>/dev/null && { python3 "${SCRIPT_DIR}/pir.py" & PIR_PID=$!; }
			fi
		elif [[ "$active" == "true" ]]; then
			stop_children
			active=false
		fi
		sleep 30
	done
}

case "${1:-daemon}" in
	daemon)  run_daemon ;;
	browser) display_power on; launch_browser ;;
	on)      display_power on ;;
	off)     stop_children ;;
	*)       echo "Usage: $0 {daemon|browser|on|off}" >&2; exit 1 ;;
esac
