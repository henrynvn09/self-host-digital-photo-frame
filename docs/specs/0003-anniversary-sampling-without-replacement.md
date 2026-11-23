# Spec 0003: Anniversary Random Sampling Without Replacement (Exhaustion Cycles)

## Problem Statement

When running in anniversary mode, the photo frame requests up to 100 photos per year across 2010–2026 within a seasonal window (±45 days around the current date). Previously, these requests performed independent, stateless random draws with replacement (`ORDER BY RANDOM() LIMIT 100`).

In everyday use, this stateless approach causes noticeable repetition:
1. Photos shown yesterday or earlier in the week frequently reappear while other photos from the same anniversary window remain unviewed for weeks or months.
2. In years with smaller libraries (e.g. 150 photos total), pure random selection with replacement repeatedly draws from the same small cluster of photos.
3. In years with large libraries (e.g. 2,000 photos in the window), users miss out on deep historical variety because there is no mechanism to guarantee that all photos in the window are seen before repeats occur.

## Solution

Transform the anniversary photo selection into a stateful **Sampling Deck** operating under **Exhaustion Cycles**:
- For each anniversary query window, only **unseen** assets are selected.
- As assets are queried and delivered, they are marked as **Dealt Assets** in the persistent database.
- Once an asset has been dealt, it is ineligible for selection until all assets in that year's anniversary window have also been dealt.
- When an anniversary window's unseen count reaches zero, that specific deck automatically resets its cycle, reshuffling all assets in that window for a fresh round.
- If a year contains fewer unseen assets than requested (e.g., 35 assets remain, but 100 were requested), it under-fills by returning only the remaining 35 unseen assets rather than prematurely repeating already dealt assets.
- As the calendar date shifts, assets aging out of the active seasonal window have their dealt status expired so they start completely fresh when that season returns next year.

## User Stories

1. As a photo frame viewer, I want today's anniversary photos to exclude photos already shown earlier this season, so that I don't see repetitive duplicates day after day.
2. As a photo frame viewer with thousands of family photos, I want the system to progressively cycle through all photos from an anniversary window before repeating, so that forgotten memories are surfaced.
3. As a photo frame viewer, I want the slideshow to pull photos across multiple historical years (2010–2026) without replacement, so that each year's memories are rotated evenly.
4. As a photo frame viewer, I want years with small photo counts (e.g. 40 photos) to return all 40 unseen photos without padding with duplicates, so that every photo shown on a given day is unique.
5. As a photo frame viewer, I want a year that has completely exhausted its photos to automatically reshuffle and start a new cycle, so that the slideshow never starves or halts.
6. As a photo frame viewer, I want years with large photo counts (e.g. 3,000 photos) to continue drawing unseen photos day after day without prematurely resetting just because another year exhausted its smaller pool.
7. As a photo frame viewer, I want playback history to survive container restarts and power outages, so that turning off the system doesn't reset the exhaustion cycles.
8. As a photo frame viewer, I want the seasonal window to shift smoothly day by day (±45 days around today), so that seasonal transitions feel gradual and natural.
9. As a photo frame viewer, I want photos that age out of the current season to have their dealt history expired, so that when the season returns next year I can enjoy them fresh again.
10. As a digital photo frame operator, I want the selection state to be tracked on the server during query delivery (deal-time), so that the client requires zero modifications or complex state sync.
11. As a digital photo frame operator, I want the API response format for random searches to remain fully compatible with standard Immich clients, so that MagicMirror modules continue operating seamlessly.
12. As a digital photo frame operator, I want database updates marking dealt assets to occur atomically in transactions, so that interrupted requests never corrupt cycle tracking.
13. As a digital photo frame operator, I want query evaluation on large libraries (10k+ assets) to execute in milliseconds using database indices, so that client slideshow requests are never delayed.
14. As a digital photo frame operator, I want pure random queries (without date bounds) to continue functioning normally without interfering with anniversary cycle tracking, so that general random modes remain supported.

## Implementation Decisions

- **Stateful Sampling Deck per Anniversary Window**: Sampling without replacement is scoped to each anniversary query window (`takenAfter` to `takenBefore`). Unseen assets are filtered by checking for null dealt timestamps.
- **Deal-Time Tracking**: Assets returned in an anniversary random query are marked as dealt in the same transaction. This decouples playback history from client connectivity and avoids complex view-confirmation protocols.
- **Under-Fill Semantic**: When the count of unseen assets in a window is less than the requested size, the query returns only the remaining unseen assets without backfilling with already dealt assets.
- **Per-Window Auto-Reset**: Reshuffle logic is evaluated per query window. If an anniversary window query finds zero unseen assets, it clears the dealt timestamps for all assets within that window and immediately executes the query against the refreshed deck.
- **Database Schema**: The assets table is augmented with a dealt timestamp column (`dealt_at`) indexed alongside capture dates to enable sub-millisecond range scans and filtering.
- **Seasonal Expiry Maintenance**: During periodic scans or anniversary queries, assets whose capture dates fall outside the active seasonal boundary have their dealt timestamps cleared or pruned.
- **Client Transparency**: No changes are required in `MMM-ImmichSlideShow` or MagicMirror; `photo-api` maintains full contract compatibility with `POST /api/search/random`.

## Testing Decisions

- **Primary Seam**: The highest level test seam is the external HTTP API endpoint `POST /api/search/random` providing `takenAfter`, `takenBefore`, and `size`.
- **Secondary Seam**: The store engine query interface accepting date boundaries and size.
- **Test Behaviors**:
  1. Verify consecutive queries within the same date window return disjoint sets of assets (no overlapping asset IDs between queries).
  2. Verify an under-fill scenario: a date range with 50 assets queried with size=30 returns 30 unique assets on call 1, the remaining 20 unique assets on call 2, and triggers an auto-reset on call 3.
  3. Verify independent cycle tracking across different years (year A exhausting does not reset year B).
  4. Verify seasonal expiration: assets outside the window are reset when their date range is queried.
- **Prior Art**: Existing integration tests in the test suite (`server_test.go` and `store_test.go`) validating API endpoints, SQLite WAL operations, and date filtering.

## Out of Scope

- Client-side view confirmation signals via WebSockets or HTTP beacon callbacks.
- User-facing manual cycle reset or "skip photo forever" rating UI.
- Applying sampling without replacement to unbounded general random searches (which remain stateless random draws).
- Multi-user distinct view history tracking (the digital photo frame represents a single shared family display).

## Interactive Visualization

The interactive simulation of the multi-round sampling permutation, tail flush boundaries, and day-to-day slice dynamics is hosted at:
- [docs/visualizations/sampling-permutation.html](file:///Users/ying/code/image-sharing/self-host-digital-photo-frame/docs/visualizations/sampling-permutation.html)

## Further Notes

- Aligns directly with Architectural Decision Record **ADR 0007 (Random Sampling Without Replacement)** and the canonical glossary definitions in `CONTEXT.md` (`Sampling Deck`, `Exhaustion Cycle`, `Dealt Asset`).
- Operates entirely within the existing lightweight SQLite database (`index.db`) on the Synology NAS.
