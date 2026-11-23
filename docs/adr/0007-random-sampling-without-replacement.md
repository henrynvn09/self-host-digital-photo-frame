# 0007. Random Sampling Without Replacement (Anniversary Exhaustion Cycle)

Date: 2026-11-23

## Status

Accepted

## Context

The digital photo frame's primary mode is `anniversary` mode, which queries `photo-api` once a day for up to 100 photos per year across 2010–2026 within a seasonal window ($\pm45$ days around current date).

Previously, queries executed naive stateless random sampling (`ORDER BY RANDOM() LIMIT 100`). In large photo libraries (e.g. 10k+ photos) or smaller yearly subsets (e.g. 150 photos), pure random sampling with replacement often picked recently displayed photos multiple days in a row while leaving hundreds of other anniversary photos unviewed.

## Decision

1. **Random Sampling Without Replacement via Exhaustion Cycles**:
   - For each anniversary query window (e.g. year $Y$ between `takenAfter` and `takenBefore`), maintain a stateful **Sampling Deck**.
   - Assets that have already been dealt in the current cycle are excluded from selection:
     ```sql
     SELECT ... FROM assets 
     WHERE taken_at >= ? AND taken_at <= ? AND dealt_at IS NULL 
     ORDER BY RANDOM() LIMIT ?
     ```
   - Returned assets are immediately marked with `dealt_at = CURRENT_TIMESTAMP` (Deal-Time tracking).

2. **Under-Fill Handling**:
   - If a deck has fewer remaining unseen assets than requested (e.g. only 35 unseen assets remain, but 100 were requested), the query returns only those 35 assets without prematurely repeating already dealt assets.

3. **Auto-Reset on Empty**:
   - When an anniversary window's unseen count reaches 0, all assets within that window have their `dealt_at` reset to `NULL`, automatically beginning a fresh Exhaustion Cycle for the next query.

4. **Seasonal Expiration**:
   - When assets age out of the current seasonal window, their `dealt_at` timestamp is pruned or ignored, guaranteeing that when that season returns the following year, the entire deck starts fresh.

5. **Storage**:
   - State is persisted directly in SQLite `index.db` in `photo-api` (`dealt_at INTEGER` column or dedicated `deck_state` table), surviving client, browser, and container restarts.

## Consequences

- **Pros**:
  - Eliminates duplicate photos across consecutive days until every photo in that year's anniversary window has been shown.
  - Gives 100% exposure to all memories in the library over time.
  - Zero client changes needed in `MMM-ImmichSlideShow` (completely backward-compatible with standard `POST /api/search/random`).
- **Cons**:
  - Adds state modification to what was previously a read-only search endpoint. Handled safely within SQLite transactions.
