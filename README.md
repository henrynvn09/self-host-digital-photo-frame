# self-host-digital-photo-frame

A lightweight, self-hosted **27-inch 2K digital photo frame** powered by a Synology NAS (`server/`) and a Raspberry Pi 3B (`client/`)—replacing heavy Immich stacks (~2–4GB RAM) with a ~20MB pure-Go photo API + MagicMirror² + motion-aware Pi client.

## Folders & Setup Guides

- **[`server/README.md`](./server/README.md)** — Complete Synology NAS Container Manager stack (`docker-compose.yml` running both [`photo-api`](./server/photo-api/README.md) and [`magicmirror`](./server/magicmirror/README.md) on a private internal Docker network).
- **[`client/README.md`](./client/README.md)** — Raspberry Pi 3B X11 kiosk setup, unified systemd `Display Schedule`, and event-driven GPIO PIR motion sensor.
- **[`PRD.md`](./PRD.md)** — Full architecture diagram, performance optimizations, and design rationale.
