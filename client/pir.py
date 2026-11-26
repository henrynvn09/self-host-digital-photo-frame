#!/usr/bin/env python3
from gpiozero import MotionSensor
import os
import signal
import subprocess
import sys
import time
from threading import Event, Timer

VCGENCMD = "/usr/bin/vcgencmd"


def load_config() -> dict:
    cfg = {"SHUTOFF_DELAY": 300, "PIR_PIN": 24, "DEBUG": False}
    cfg_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.conf")
    if os.path.exists(cfg_path):
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
    if os.environ.get("PIR_DEBUG", "").lower() == "true" or "--test" in sys.argv:
        cfg["DEBUG"] = True
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
    debug = cfg["DEBUG"]

    def log(msg: str) -> None:
        if debug:
            print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] [PIR] {msg}", flush=True)

    try:
        pir = MotionSensor(cfg["PIR_PIN"])
    except Exception as exc:
        sys.stderr.write(
            f"[PIR] ERROR: Failed to open GPIO {cfg['PIR_PIN']} ({exc}). "
            "Verify PIR_GPIO_PIN in config.conf, install python3-lgpio/python3-gpiozero, "
            "and ensure your user is in the 'gpio' group (sudo usermod -a -G gpio $USER).\n"
        )
        return 1

    state = {"off": is_display_off(), "timer": None, "running": True}
    print(
        f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] [PIR] Started on GPIO {cfg['PIR_PIN']} "
        f"(timeout={delay}s, display_off={state['off']}, debug={debug})",
        flush=True,
    )

    def cancel_timer():
        if state["timer"] is not None:
            state["timer"].cancel()
            state["timer"] = None

    def power_off_if_idle():
        if not pir.motion_detected and not state["off"]:
            log(f"Idle timeout ({delay}s) reached -> turning display OFF")
            if display_power(0):
                state["off"] = True
            else:
                log("WARNING: display_power(0) failed (check DISPLAY=:0 / xset / vcgencmd)")
        state["timer"] = None

    def on_motion():
        cancel_timer()
        log("Motion detected")
        if state["off"]:
            if display_power(1):
                state["off"] = False
                log("Display turned ON")
            else:
                log("WARNING: display_power(1) failed (check DISPLAY=:0 / xset / vcgencmd)")

    def on_no_motion():
        cancel_timer()
        log(f"Motion stopped -> scheduling display OFF in {delay}s")
        t = Timer(delay, power_off_if_idle)
        t.daemon = True
        t.start()
        state["timer"] = t

    stop_event = Event()

    def on_stop(_sig, _frame):
        log("Stopping PIR sensor")
        state["running"] = False
        cancel_timer()
        stop_event.set()

    signal.signal(signal.SIGTERM, on_stop)
    signal.signal(signal.SIGINT, on_stop)
    pir.when_motion = on_motion
    pir.when_no_motion = on_no_motion

    try:
        while state["running"]:
            if stop_event.wait(3600):
                break
            cfg = load_config()
            delay = cfg["SHUTOFF_DELAY"]
            debug = cfg["DEBUG"]
    finally:
        cancel_timer()
        pir.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
