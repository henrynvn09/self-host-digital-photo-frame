#!/bin/bash
export DISPLAY="${DISPLAY:-:0}"
if command -v xset >/dev/null 2>&1; then
	xset -display "$DISPLAY" dpms force on 2>/dev/null || true
	xset -display "$DISPLAY" s off -dpms s noblank 2>/dev/null || true
fi
if command -v /usr/bin/vcgencmd >/dev/null 2>&1; then
	/usr/bin/vcgencmd display_power 1 >/dev/null 2>&1 || true
fi
