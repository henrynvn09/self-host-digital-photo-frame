# Unified repository with `server/` and `client/` folders and single Compose stack

Previously, the Raspberry Pi client and MagicMirror config lived in `digital-photoframe` (using git submodules for 3rd-party modules) while the photo backend was separate. Consolidating into two top-level folders—`server/` (containing `docker-compose.yml`, `magicmirror/`, and `photo-api/`) and `client/` (Raspberry Pi 3B scripts)—lets Synology Container Manager deploy both `magicmirror` and `photo-api` on a private internal bridge network in one step from `server/`. Building a thin `magicmirror` Dockerfile extending a pinned `karsten13/magicmirror` image with commit-pinned modules removes git submodule friction and manual `npm install` steps on the NAS.

## Consequences

- `photo-api` does not need to expose any port to the LAN in production; only `magicmirror` exposes port `8036`.
- `server/` and `client/` (plus `server/photo-api/` and `server/magicmirror/`) each maintain their own `README.md` setup guide so server and client setup remain independently followable.
- Original `digital-photoframe` remains untouched as an archival reference.
