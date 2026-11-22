#!/bin/bash
# MagicMirror Manual Startup Script
#
# NOTE: This script is for MANUAL/DEBUG USE ONLY
# For automatic scheduling, use systemd timers (configured via setup_client.sh)
#
# This script:
# - Starts MagicMirror client in client-only mode
# - Starts PIR motion sensor for display control
# - Monitors processes and auto-restarts PIR if it crashes
#
# Usage:
#   ./turn_on_magic_mirror.sh          # Start MagicMirror manually
#
# To stop:
#   ./turn_off_magic_mirror.sh         # Stop MagicMirror manually

set -euo pipefail
IFS=$'\n\t'

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOME_DIR="${HOME}"
MM_DIR="${HOME_DIR}/MagicMirror"
CONFIG_FILE="${SCRIPT_DIR}/config.conf"
PID_DIR="/tmp"
LOCK_DIR="/tmp/mm_instance.lock"
PID_FILE="${PID_DIR}/mm_pids.txt"

# Default values (fallback if config missing)
ADDRESS="192.168.4.45"
PORT="8036"

# Load server settings from config file if available
if [[ -f "$CONFIG_FILE" ]]; then
	# shellcheck disable=SC1090
	source "$CONFIG_FILE" 2>/dev/null || true
	# Use SERVER_IP/SERVER_PORT from config if set
	ADDRESS="${SERVER_IP:-$ADDRESS}"
	PORT="${SERVER_PORT:-$PORT}"
fi

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# Acquire lock (mkdir is atomic)
if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
	log "Startup aborted: lock exists (${LOCK_DIR}). Another instance may be running."
	exit 0
fi

cleanup() {
	local code=$?
	log "Cleanup initiated (exit code ${code})"

	# Remove lock first to allow future starts even if kills fail
	[[ -d "${LOCK_DIR}" ]] && rmdir "${LOCK_DIR}" 2>/dev/null || true

	# Read PIDs from file
	if [[ -f "${PID_FILE}" ]]; then
		local npm_pid electron_pid pir_pid
		npm_pid=$(grep "^NPM:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		electron_pid=$(grep "^ELECTRON:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		pir_pid=$(grep "^PIR:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		
		# Stop PIR first
		if [[ -n "${pir_pid}" ]] && kill -0 "${pir_pid}" 2>/dev/null; then
			log "Stopping PIR control (PID ${pir_pid})"
			kill "${pir_pid}" 2>/dev/null || true
			sleep 1
			if kill -0 "${pir_pid}" 2>/dev/null; then
				kill -9 "${pir_pid}" 2>/dev/null || true
			fi
		fi
		
		# Stop electron
		if [[ -n "${electron_pid}" ]] && kill -0 "${electron_pid}" 2>/dev/null; then
			log "Stopping electron (PID ${electron_pid})"
			kill "${electron_pid}" 2>/dev/null || true
			sleep 2
			if kill -0 "${electron_pid}" 2>/dev/null; then
				log "Force killing electron (PID ${electron_pid})"
				kill -9 "${electron_pid}" 2>/dev/null || true
			fi
		fi
		
		# Stop npm parent
		if [[ -n "${npm_pid}" ]] && kill -0 "${npm_pid}" 2>/dev/null; then
			log "Stopping npm (PID ${npm_pid})"
			kill "${npm_pid}" 2>/dev/null || true
		fi
		
		rm -f "${PID_FILE}"
	fi

	log "Cleanup complete"
}
trap cleanup EXIT INT TERM

validate_old_processes() {
	# Clean up any old PID files or running processes
	if [[ -f "${PID_FILE}" ]]; then
		log "Found existing PID file, cleaning up old processes..."
		local npm_pid electron_pid pir_pid
		npm_pid=$(grep "^NPM:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		electron_pid=$(grep "^ELECTRON:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		pir_pid=$(grep "^PIR:" "${PID_FILE}" 2>/dev/null | cut -d: -f2)
		
		# Kill PIR if running
		if [[ -n "${pir_pid}" ]] && kill -0 "${pir_pid}" 2>/dev/null; then
			log "Stopping old PIR process (PID ${pir_pid})"
			kill -9 "${pir_pid}" 2>/dev/null || true
		fi
		
		# Kill electron if running
		if [[ -n "${electron_pid}" ]] && kill -0 "${electron_pid}" 2>/dev/null; then
			log "Stopping old electron process (PID ${electron_pid})"
			kill -9 "${electron_pid}" 2>/dev/null || true
		fi
		
		# Kill npm if running
		if [[ -n "${npm_pid}" ]] && kill -0 "${npm_pid}" 2>/dev/null; then
			log "Stopping old npm process (PID ${npm_pid})"
			kill -9 "${npm_pid}" 2>/dev/null || true
		fi
		
		rm -f "${PID_FILE}"
	fi
	
	# Also check for old format PID files
	if [[ -f "/tmp/mm.pid" ]]; then
		local old_pid
		old_pid=$(cat /tmp/mm.pid 2>/dev/null || true)
		if [[ -n "${old_pid}" ]] && kill -0 "${old_pid}" 2>/dev/null; then
			log "Stopping old MagicMirror process from legacy PID file (PID ${old_pid})"
			kill -9 "${old_pid}" 2>/dev/null || true
		fi
		rm -f /tmp/mm.pid
	fi
	
	if [[ -f "/tmp/pir.pid" ]]; then
		local old_pid
		old_pid=$(cat /tmp/pir.pid 2>/dev/null || true)
		if [[ -n "${old_pid}" ]] && kill -0 "${old_pid}" 2>/dev/null; then
			log "Stopping old PIR process from legacy PID file (PID ${old_pid})"
			kill -9 "${old_pid}" 2>/dev/null || true
		fi
		rm -f /tmp/pir.pid
	fi
}

# Pre-start validation: clear old processes
validate_old_processes

# Check server connectivity before starting
log "Checking MagicMirror server connectivity..."
if ! "${SCRIPT_DIR}/check_server.sh" 5 3; then
	log "ERROR: Cannot reach MagicMirror server at ${ADDRESS}:${PORT}"
	exit 1
fi

# Turn on display when starting viewer (Bookworm X11 DPMS + vcgencmd fallback)
"${SCRIPT_DIR}/pir-control-display/turn_on_display.sh" || true

log "Starting frame viewer connecting to ${ADDRESS}:${PORT}..."
export DISPLAY="${DISPLAY:-:0}"

"${SCRIPT_DIR}/mm.sh" &
ELECTRON_PID=$!
NPM_PID=$ELECTRON_PID

cat > "${PID_FILE}" << EOF
NPM:${NPM_PID}
ELECTRON:${ELECTRON_PID}
EOF
log "Frame viewer started (PID ${ELECTRON_PID})"

# Give viewer a moment to initialize before starting PIR
sleep 2

# Verify electron is still running
if ! kill -0 "${ELECTRON_PID}" 2>/dev/null; then
	log "ERROR: Electron process died immediately after start!"
	log "Check output above for errors or run with journalctl if using systemd"
	exit 1
fi

# Start PIR controller
if command -v python3 >/dev/null 2>&1; then
	python3 "${SCRIPT_DIR}/pir-control-display/pir.py" &
	PIR_PID=$!
	
	# Append PIR PID to same file
	echo "PIR:${PIR_PID}" >> "${PID_FILE}"
	log "PIR control started (PID ${PIR_PID})"
else
	log "Warning: python3 not found, PIR control not started"
fi

# Monitor both processes - keep script alive
log "Monitoring processes (npm: ${NPM_PID}, electron: ${ELECTRON_PID}, PIR: ${PIR_PID:-none})..."

while true; do
	# Check if electron is still running (main process)
	if ! kill -0 "${ELECTRON_PID}" 2>/dev/null; then
		log "ERROR: Electron process (PID ${ELECTRON_PID}) has exited unexpectedly!"
		log "Check output above for errors or run with journalctl if using systemd"
		
		# Clean up npm parent if still running
		if [[ "${NPM_PID}" != "${ELECTRON_PID}" ]] && kill -0 "${NPM_PID}" 2>/dev/null; then
			log "Stopping orphaned npm process (PID ${NPM_PID})"
			kill "${NPM_PID}" 2>/dev/null || true
		fi
		
		exit 1
	fi
	
	# Check PIR if it was started
	if [[ -n "${PIR_PID:-}" ]] && ! kill -0 "${PIR_PID}" 2>/dev/null; then
		log "WARNING: PIR process (PID ${PIR_PID}) has exited, restarting..."
		python3 "${SCRIPT_DIR}/pir-control-display/pir.py" &
		PIR_PID=$!
		
		# Update PID file (remove old PIR line, add new one)
		grep -v "^PIR:" "${PID_FILE}" > "${PID_FILE}.tmp" 2>/dev/null || true
		echo "PIR:${PIR_PID}" >> "${PID_FILE}.tmp"
		mv "${PID_FILE}.tmp" "${PID_FILE}"
		
		log "PIR control restarted (PID ${PIR_PID})"
	fi
	
	# Sleep for 30 seconds before next check
	sleep 30
done
