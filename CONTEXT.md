# Photoframe

A self-hosted digital photo frame system consisting of a NAS-hosted photo indexing and serving service, a MagicMirror display server, and a motion-aware Raspberry Pi display client.

## Language

### Photo Library & Indexing

**Asset**:
A single indexed image file in the library, identified by a deterministic path-based ID and described by its capture date, geographic location, and optional album.
_Avoid_: Photo, image, file, item

**Library**:
The read-only root directory tree on the NAS containing all original source assets.
_Avoid_: Photo folder, volume, storage, bucket

**Scan**:
An incremental pass over the library that adds new assets, updates changed assets, and removes deleted assets from the index while skipping system and excluded directories.
_Avoid_: Sync, crawl, import, ingest

**Preview**:
A display-sized (2560×1440 default), EXIF-rotated JPEG rendition generated lazily on demand from an asset and persisted in a bounded LRU disk cache.
_Avoid_: Thumbnail, resized image, variant

**Album**:
A human-readable event or trip label derived from an asset's immediate parent directory name, omitted when that directory name is purely numeric.
_Avoid_: Folder, collection, category, tag

**Sampling Deck**:
A scoped subset of assets matching an anniversary query window from which unseen assets are drawn randomly without replacement.
_Avoid_: Bucket, playlist, photo queue, batch

**Exhaustion Cycle**:
The operational cycle during which unseen assets in a sampling deck are progressively dealt until 0 unseen assets remain, triggering an automatic reshuffle.
_Avoid_: Reset round, replenishment, reload, wipe

**Dealt Asset**:
An asset that has been selected and returned by an anniversary query during the current exhaustion cycle, marked to prevent duplicate appearances until cycle reset.
_Avoid_: Seen photo, viewed asset, played item

### Display & Client

**Frame Client**:
The Raspberry Pi 3B kiosk running MagicMirror in client-only X11 mode connected to the NAS server over LAN.
_Avoid_: Display box, Pi server, kiosk browser

**Display Schedule**:
The configured weekday and weekend active hours during which the frame client runs MagicMirror and responds to motion detection.
_Avoid_: Cron window, power timer, uptime rules

**Widget Feed**:
An external data source (Google Calendar iCal feed or Google Spreadsheet CSV export) polled on a configurable interval to render overlay widgets on the frame.
_Avoid_: Data plugin, third-party sync, remote API

**Live Config**:
Runtime settings in the mounted configuration directory (`config.js`, `custom.css`, and `config.conf`) that apply dynamically without manually restarting containers or system services.
_Avoid_: Hot patch, dynamic env, runtime override
