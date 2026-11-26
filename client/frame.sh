#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${SCRIPT_DIR}/config.conf"

SERVER_IP="${SERVER_IP:-192.168.4.45}"
SERVER_PORT="${SERVER_PORT:-8036}"
MONDAY_FRIDAY="${MONDAY_FRIDAY:-16:00-20:45}"
SATURDAY_SUNDAY="${SATURDAY_SUNDAY:-08:00-20:45}"
US_HOLIDAYS="${US_HOLIDAYS:-true}"
DEBUG="${DEBUG:-false}"
TARGET_URL=""

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] [INFO] $*"; }
debug_log() {
	if [[ "$DEBUG" == "true" || "$DEBUG" == "TRUE" ]]; then
		echo "[$(date '+%Y-%m-%d %H:%M:%S')] [DEBUG] $*"
	fi
}

load_config() {
	if [[ -f "$CONFIG_FILE" ]]; then
		# shellcheck disable=SC1090
		source "$CONFIG_FILE"
	fi
	TARGET_URL="http://${SERVER_IP}:${SERVER_PORT}"
	debug_log "Loaded config from ${CONFIG_FILE}: SERVER=${TARGET_URL}, MON_FRI=${MONDAY_FRIDAY}, SAT_SUN=${SATURDAY_SUNDAY}, US_HOLIDAYS=${US_HOLIDAYS}, DEBUG=${DEBUG}"
}

probe_url() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsS --max-time 2 "${TARGET_URL}" >/dev/null 2>&1
	elif command -v nc >/dev/null 2>&1; then
		nc -z -w 2 "${SERVER_IP}" "${SERVER_PORT}" >/dev/null 2>&1
	else
		timeout 2 bash -c "echo > /dev/tcp/${SERVER_IP}/${SERVER_PORT}" 2>/dev/null
	fi
}

check_server() {
	local max_attempts="${1:-5}"
	local delay="${2:-3}"
	for i in $(seq 1 "$max_attempts"); do
		log "Checking server ${TARGET_URL} (attempt ${i}/${max_attempts})..."
		if probe_url; then
			log "Server ${TARGET_URL} is reachable."
			return 0
		fi
		if [[ $i -lt $max_attempts ]]; then
			sleep "$delay"
		fi
	done
	log "WARNING: Server ${TARGET_URL} unreachable after ${max_attempts} attempts."
	return 1
}

load_config
export DISPLAY="${DISPLAY:-:0}"
KIOSK_PID=""
PIR_PID=""

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

# Returns 0 if the date (default: today) is a US federal holiday (actual or observed weekday).
is_us_holiday() {
	local m=$((10#${1:-$(date +%m)}))
	local d=$((10#${2:-$(date +%d)}))
	local dow=$((10#${3:-$(date +%u)})) # 1=Mon .. 7=Sun

	# Fixed-date holidays (including Friday/Monday observed dates)
	case "${m}-${d}" in
		1-1|6-19|7-4|11-11|12-25) return 0 ;;
	esac
	if [[ $dow -eq 5 ]]; then
		case "${m}-${d}" in
			12-31|6-18|7-3|11-10|12-24) return 0 ;;
		esac
	elif [[ $dow -eq 1 ]]; then
		case "${m}-${d}" in
			1-2|6-20|7-5|11-12|12-26) return 0 ;;
		esac
	fi

	# Floating holidays (MLK, Presidents, Memorial, Labor, Columbus, Thanksgiving)
	if [[ $dow -eq 1 ]]; then
		[[ $m -eq 1 && $d -ge 15 && $d -le 21 ]] && return 0  # MLK Day (3rd Mon Jan)
		[[ $m -eq 2 && $d -ge 15 && $d -le 21 ]] && return 0  # Presidents' Day (3rd Mon Feb)
		[[ $m -eq 5 && $d -ge 25 && $d -le 31 ]] && return 0  # Memorial Day (last Mon May)
		[[ $m -eq 9 && $d -ge 1  && $d -le 7  ]] && return 0  # Labor Day (1st Mon Sep)
		[[ $m -eq 10 && $d -ge 8 && $d -le 14 ]] && return 0  # Columbus Day (2nd Mon Oct)
	elif [[ $dow -eq 4 ]]; then
		[[ $m -eq 11 && $d -ge 22 && $d -le 28 ]] && return 0 # Thanksgiving (4th Thu Nov)
	fi

	return 1
}

in_schedule() {
	local range="$MONDAY_FRIDAY"
	local day_num
	day_num=$(date +%u)
	if [[ $day_num -ge 6 ]] || { [[ "$US_HOLIDAYS" == "true" || "$US_HOLIDAYS" == "TRUE" ]] && is_us_holiday; }; then
		range="$SATURDAY_SUNDAY"
	fi
	local on_str="${range%%-*}" off_str="${range##*-}"
	local now_min=$((10#$(date +%H) * 60 + 10#$(date +%M)))
	local on_min=$((10#${on_str%%:*} * 60 + 10#${on_str##*:}))
	local off_min=$((10#${off_str%%:*} * 60 + 10#${off_str##*:}))
	debug_log "Schedule check: day=${day_num}, range=${range}, now_min=${now_min}, window=[${on_min}, ${off_min})"
	[[ $now_min -ge $on_min && $now_min -lt $off_min ]]
}

start_children() {
	check_server 3 2 || true
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
	log "Schedule daemon started (Mon-Fri ${MONDAY_FRIDAY}, Sat-Sun/Holidays ${SATURDAY_SUNDAY}, US_HOLIDAYS=${US_HOLIDAYS}, server ${TARGET_URL})"
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
	daemon)   run_daemon ;;
	browser)  display_power on; launch_browser ;;
	pir-test) PIR_DEBUG=true exec python3 "${SCRIPT_DIR}/pir.py" --test ;;
	check)    check_server "${2:-5}" "${3:-3}" ;;
	on)       display_power on ;;
	off)      stop_children ;;
	*)        echo "Usage: $0 {daemon|browser|pir-test|check|on|off}" >&2; exit 1 ;;
esac
