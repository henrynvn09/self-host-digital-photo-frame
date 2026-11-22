# Read NAS folders directly (read-only) instead of querying Synology Photos API

Synology Photos exposes no official public API; community integrations rely on reverse-engineered internal endpoints (`SYNO.Foto.*`) that break across DSM updates, require session/2FA token maintenance, and cap thumbnails around ~1280px (soft on a 27" 2K monitor). Mounting the Synology photo library read-only directly into the container eliminates DSM auth fragility, guarantees originals cannot be modified, and lets `server-photo-api` generate native 2560×1440 previews.

## Consequences

- Synology Photos virtual albums are not visible; `Album` names are derived from directory names instead.
- `server-photo-api` must run as a DSM-compatible `PUID:PGID` and explicitly skip Synology system directories (`@eaDir`, `#recycle`).
