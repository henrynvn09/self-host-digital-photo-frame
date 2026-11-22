# Expose an Immich-compatible REST subset instead of a custom photo API

`digital-photoframe` already runs `MMM-ImmichSlideShow`, which supports cross-year anniversary windows, random pools, date-range queries, and metadata overlays (`date`, `geo`, `count`, `album`). Rather than rewriting or replacing the MagicMirror frontend module—or running full Immich (Postgres, Redis, ML containers)—`server-photo-api` implements the small subset of Immich REST endpoints that `MMM-ImmichSlideShow` calls, omitting smart/ML search.

## Consequences

- `digital-photoframe` requires only a URL/config update, with zero custom MagicMirror module maintenance.
- `server-photo-api` is coupled to the response shape of `MMM-ImmichSlideShow`'s expected Immich API version; upgrading `MMM-ImmichSlideShow` later may require updating endpoint schemas.
