#!/usr/bin/env python3
from gpiozero import MotionSensor
import os
import signal
import subprocess
import time
from threading import Timer

VCGENCMD = "/usr/bin/vcgencmd"


def load_config() -> dict:
    cfg = {"SHUTOFF_DELAY": 300, "PIR_PIN": 24, "DEBUG": False}
    cfg_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.conf")
    if not os.path.exists(cfg_path):
        return cfg
    raw = {}
    with open(cfg_path, "r") as f:
        for line in f:
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                raw[k.strip()] = v.strip().strip('"').strip("'")
    if raw.get("PIR_TIMEOUT_MINUTES", "").isdigit():
        m = int(raw["PIR_TIMEOUT_MINUTES"])
        if m > 0:
            cfg["SHUTOFF_DELAY"] = m * 60
    if raw.get("PIR_GPIO_PIN", "").isdigit():
        p = int(raw["PIR_GPIO_PIN"])
        if 1 <= p <= 27:
            cfg["PIR_PIN"] = p
    cfg["DEBUG"] = raw.get("DEBUG", "").lower() == "true"
    return cfg


def display_power(state: int) -> bool:
    display = os.environ.get("DISPLAY", ":0")
    ok = False
    try:
        cmd = "on" if state == 1 else "off"
        subprocess.run(
            ["xset", "-display", display, "dpms", "force", cmd],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
        )
        ok = True
    except (subprocess.CalledProcessError, FileNotFoundError):
        pass
    try:
        subprocess.run(
            [VCGENCMD, "display_power", str(state)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
        )
        ok = True
    except (subprocess.CalledProcessError, FileNotFoundError):
        pass
    return ok


def is_display_off() -> bool:
    display = os.environ.get("DISPLAY", ":0")
    try:
        res = subprocess.run(
            ["xset", "-display", display, "q"],
            capture_output=True,
            text=True,
            check=True,
        )
        if "Monitor is On" in res.stdout:
            return False
        if "Monitor is Off" in res.stdout:
            return True
    except Exception:
        pass
    try:
        res = subprocess.run(
            [VCGENCMD, "display_power"], capture_output=True, text=True, check=True
        )
        if "display_power=1" in res.stdout:
            return False
    except Exception:
        pass
    return True


def main() -> int:
    cfg = load_config()
    delay = cfg["SHUTOFF_DELAY"]
    pir = MotionSensor(cfg["PIR_PIN"])
    state = {"off": is_display_off(), "timer": None, "running": True}

    def cancel_timer():
        if state["timer"] is not None:
            state["timer"].cancel()
            state["timer"] = None

    def power_off_if_idle():
        if not pir.motion_detected and not state["off"]:
            if display_power(0):
                state["off"] = True
        state["timer"] = None

    def on_motion():
        cancel_timer()
        if state["off"]:
            if display_power(1):
                state["off"] = False

    def on_no_motion():
        cancel_timer()
        t = Timer(delay, power_off_if_idle)
        t.daemon = True
        t.start()
        state["timer"] = t

    def on_stop(_sig, _frame):
        state["running"] = False
        cancel_timer()

    signal.signal(signal.SIGTERM, on_stop)
    signal.signal(signal.SIGINT, on_stop)
    pir.when_motion = on_motion
    pir.when_no_motion = on_no_motion

    try:
        while state["running"]:
            time.sleep(3600)
    finally:
        cancel_timer()
        pir.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
