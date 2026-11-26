#!/usr/bin/env python3
import os
import signal
import sys

# Disable WebKitGTK GPU compositing/DMA-BUF on 1 GB Raspberry Pi X11:
# avoids massive shared-memory spikes (OOM freezes) and missing foreground <img> layers.
os.environ.setdefault("WEBKIT_DISABLE_COMPOSITING_MODE", "1")
os.environ.setdefault("WEBKIT_DISABLE_DMABUF_RENDERER", "1")

import gi  # noqa: E402

gi.require_version("Gtk", "3.0")
gi.require_version("Gdk", "3.0")
try:
    gi.require_version("WebKit2", "4.1")
except ValueError:
    gi.require_version("WebKit2", "4.0")

from gi.repository import Gdk, GLib, Gtk, WebKit2  # noqa: E402


def build_kiosk(url: str) -> Gtk.Window:
    ctx = WebKit2.WebContext.get_default()
    ctx.set_cache_model(WebKit2.CacheModel.DOCUMENT_VIEWER)

    settings = WebKit2.Settings()
    settings.set_enable_webgl(False)
    settings.set_enable_smooth_scrolling(False)
    settings.set_enable_page_cache(False)
    if hasattr(settings, "set_hardware_acceleration_policy"):
        settings.set_hardware_acceleration_policy(
            WebKit2.HardwareAccelerationPolicy.NEVER
        )

    webview = WebKit2.WebView.new_with_context(ctx)
    webview.set_settings(settings)
    webview.override_background_color(
        Gtk.StateFlags.NORMAL, Gdk.RGBA(0, 0, 0, 1)
    )

    def on_load_failed(_view, _event, failing_uri, _error):
        sys.stderr.write(f"[kiosk] load failed for {failing_uri}, retrying in 5s\n")
        GLib.timeout_add_seconds(5, lambda: (webview.load_uri(url), False)[1])
        return True

    def on_web_process_terminated(_view, _reason):
        sys.stderr.write("[kiosk] web process terminated, reloading in 2s\n")
        GLib.timeout_add_seconds(2, lambda: (webview.load_uri(url), False)[1])

    webview.connect("load-failed", on_load_failed)
    if hasattr(webview, "connect"):
        try:
            webview.connect("web-process-terminated", on_web_process_terminated)
        except TypeError:
            pass

    win = Gtk.Window(type=Gtk.WindowType.TOPLEVEL)
    win.set_title("DigitalPhotoFrame")
    win.set_decorated(False)
    win.fullscreen()
    win.override_background_color(
        Gtk.StateFlags.NORMAL, Gdk.RGBA(0, 0, 0, 1)
    )
    win.connect("destroy", Gtk.main_quit)

    def on_realize(widget):
        gdk_win = widget.get_window()
        if gdk_win is not None:
            cursor = Gdk.Cursor.new_for_display(
                gdk_win.get_display(), Gdk.CursorType.BLANK_CURSOR
            )
            gdk_win.set_cursor(cursor)

    win.connect("realize", on_realize)
    win.add(webview)
    webview.load_uri(url)
    win.show_all()
    return win


def main() -> int:
    url = sys.argv[1] if len(sys.argv) > 1 else os.environ.get("KIOSK_URL", "http://127.0.0.1:8036")
    GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGINT, Gtk.main_quit)
    GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, signal.SIGTERM, Gtk.main_quit)
    build_kiosk(url)
    Gtk.main()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
