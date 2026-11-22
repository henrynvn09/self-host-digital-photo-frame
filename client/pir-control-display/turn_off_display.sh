#!/bin/bash
export DISPLAY="${DISPLAY:-:0}"
if command -v xset >/dev/null 2>&1; then
	xset -display "$DISPLAY" dpms force off 2>/dev/null || true
fi
if command -v /usr/bin/vcgencmd >/dev/null 2>&1; then
	/usr/bin/vcgencmd display_power 0 >/dev/null 2>&1 || true
fi
