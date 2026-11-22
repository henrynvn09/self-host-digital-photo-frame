# Unified repository with root Docker Compose and thin MagicMirror image

Previously, the Raspberry Pi client and MagicMirror config lived in `digital-photoframe` (using git submodules for 3rd-party modules) while the photo backend was separate. Consolidating `client/`, `magicmirror/`, and `photo-api/` into one repository with a single root `docker-compose.yml` lets Synology Container Manager deploy both `magicmirror` and `photo-api` on a private internal bridge network in one step. Building a thin `magicmirror` Dockerfile extending `karsten13/magicmirror` with pinned modules removes git submodule friction and manual `npm install` steps on the NAS.

## Consequences

- `photo-api` does not need to expose any port to the LAN in production; only `magicmirror` exposes port `8036`.
- Each top-level folder (`client/`, `magicmirror/`, `photo-api/`) maintains its own `README.md` setup guide so client and server setup remain independently followable.
- Original `digital-photoframe` remains untouched as an archival reference.
