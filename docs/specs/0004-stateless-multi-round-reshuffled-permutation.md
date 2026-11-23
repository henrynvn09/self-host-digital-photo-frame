# Spec 0004: Stateless Multi-Round Reshuffled Permutation with Seamless Straddle Stitch

## Problem Statement

When viewing photos in anniversary mode, the digital photo frame queries `photo-api` once a day for up to 100 photos per historical year (2010–2026) within a seasonal window ($\pm45$ days around the current date). While the initial deterministic permutation eliminated write locks and guaranteed non-overlapping slices via natural UUIDv5 ordering (`ORDER BY id ASC`), it exhibited a repetitive cyclical flaw:

1. **Cycle Repetition**: Once an exhaustion cycle of $N$ photos finished and wrapped around, the subsequent cycle began again at the beginning of the same static list (`0..99`), displaying the exact same batch of photos in the exact same sequence.
2. **Under-Fill Stagnation**: In years with smaller libraries ($N \le K$, e.g. 35 photos where $K=100$), every single day received the identical, static sequence of 35 photos.
3. **Partial Drops vs Full Batches**: Viewers expect slideshows to always deliver the full requested batch size ($K$), even on transition days that straddle the end of one cycle and the beginning of another.
4. **Desire for True Variation**: Viewers want the sampling to be truly varied across cycles: after one full round finishes, the entire pool should be freshly reshuffled in a completely different order, while strictly maintaining zero duplicates across consecutive days within each round.

## Solution

Implement **Stateless Multi-Round Reshuffled Permutation with Seamless Straddle Stitch**:

1. **Infinite Concatenated Virtual Tape**:
   - The playback timeline is structured as an unbroken continuum of rounds:
     $$\text{Round 0 (Seed 0)} \longrightarrow \text{Round 1 (Seed 1)} \longrightarrow \text{Round 2 (Seed 2)} \dots$$
2. **Deterministic Fisher-Yates Shuffle per Round**:
   - For each round $R$, the entire pool of $N$ asset IDs is deterministically permuted using a PRNG seeded by:
     $$\text{Seed} = \text{hash}(\text{currentYear}, \text{anniversaryWindow}, R)$$
   - Consecutive days within round $R$ draw disjoint slices without replacement.
   - When advancing to round $R+1$, a new seed reshuffles all $N$ photos into a completely fresh order.
3. **Seamless Straddle Stitch**:
   - If a daily request straddles the boundary between round $R$ and round $R+1$ ($\text{offset} + K > N$):
     - The first piece draws the remaining $N - \text{offset}$ photos from Round $R$, cleanly exhausting Round $R$.
     - The second piece draws the first $K - (N - \text{offset})$ photos from Round $R+1$'s fresh shuffle.
     - The client always receives the **full batch size $K$** with zero missing photos.
4. **Intra-Day Deduplication**:
   - During a straddle transition, any item in Round $R+1$'s head that already appeared in Round $R$'s tail is skipped, ensuring 100% uniqueness within that day's batch.
5. **Under-Fill Dynamic Shuffling**:
   - When $N \le K$, the query returns all $N$ photos shuffled with a fresh seed each day ($R = D - 1$), ensuring even small libraries show dynamic, ever-changing photo orders daily.
6. **Zero Database Writes**:
   - Preserves 100% read-only operation with zero database write locks, zero WAL churn, and perfect synchronization across multiple digital photo frames in different rooms.

## User Stories

1. As a digital photo frame viewer, I want anniversary photos to appear in a fresh, randomized order on every new cycle, so that I don't see the exact same sequence of photos repeating round after round.
2. As a digital photo frame viewer, I want consecutive days within the same round to show completely non-overlapping photos, so that I never see yesterday's photos repeated today.
3. As a digital photo frame viewer, I want every single day to display the full requested batch of photos (e.g. 100 photos), so that transition days between cycles don't suddenly show half-empty slideshows.
4. As a digital photo frame viewer, I want transition days to finish the previous cycle cleanly and pull the remainder from the next freshly shuffled cycle, so that every photo in the library is seen before repeating.
5. As a digital photo frame viewer with older, sparse photo years (e.g. 35 photos from 2011), I want all 35 photos to be shown every day in a brand-new random order, so that small collections never look static or frozen.
6. As a digital photo frame viewer, I want the shuffle sequence to vary year over year (e.g. 2026 vs 2027), so that next year's anniversary season introduces a fresh viewing experience for the same photos.
7. As a digital photo frame viewer, I want different anniversary years (e.g. 2015 vs 2020) to have independent random seeds, so that different years don't mirror each other's shuffle patterns.
8. As a digital photo frame viewer with a large photo library (e.g. 3,000 photos in 2022), I want the system to deliver 30 consecutive days of completely non-repeating photos before starting a newly shuffled round.
9. As a digital photo frame viewer, I want any straddle day that bridges two cycles to automatically deduplicate photos, so that no single photo appears twice on the same day.
10. As a digital photo frame viewer, I want multiple photo frames in different rooms to display the identical synchronized batch of photos for that day, so that the family enjoys a shared daily experience.
11. As a digital photo frame operator, I want the sampling engine to perform zero database writes during slideshow playback, so that the NAS SQLite database experiences zero lock contention or disk wear.
12. As a digital photo frame operator, I want the system to survive client, container, or server restarts without losing its place in the cycle, so that power outages never reset playback order.
13. As a digital photo frame operator, I want query response times to remain under 5 milliseconds on 10,000+ photo libraries, so that slideshow fetches on Raspberry Pi frames never lag or timeout.
14. As a digital photo frame operator, I want the API contract to remain 100% compatible with Immich and `MMM-ImmichSlideShow`, so that no frontend modules or configurations need modifications.
15. As a digital photo frame operator, I want unbounded random queries (without date filters) to continue using dynamic random sampling, so that general library shuffle modes continue working as expected.
16. As a digital photo frame operator, I want the system to support Leap Years (Day 366) gracefully, so that leap days maintain continuous cycle math without crashes or index errors.
17. As a digital photo frame operator, I want newly added or scanned photos to integrate naturally into future rounds, so that library expansions do not corrupt the virtual tape.
18. As a digital photo frame operator, I want zero dependency on external random services or complex caching daemons, so that the single binary remains lightweight and self-contained.
19. As an automated test runner, I want deterministic test functions that accept an explicit day-of-year and year, so that multi-day and multi-round cycle progressions can be verified instantly without clock manipulation.
20. As a system architect, I want the mathematical specification of the virtual tape and straddle logic to be verifiable through pure functional tests, ensuring long-term maintainability.

## Implementation Decisions

- **Virtual Concatenated Tape Abstraction**:
  - Calculate global daily position:
    $$\text{globalStart} = (D - 1) \times K$$
  - Determine active round and intra-round offset:
    $$\text{round}_1 = \lfloor \text{globalStart} / N \rfloor$$
    $$\text{offset}_1 = \text{globalStart} \pmod N$$
- **Multi-Key Deterministic Seed Derivation**:
  - Compute a 64-bit seed using FNV-1a hashing:
    $$\text{Seed} = \text{hash}(\text{currentYear} : \text{anniversaryWindow} : \text{round})$$
  - Isolates calendar years, date boundaries, and rounds from RNG alignment.
- **In-Memory Fisher-Yates Permutation**:
  - Execute a fast index-only scan to fetch sorted asset IDs in the anniversary window:
    ```sql
    SELECT id FROM assets WHERE taken_at >= ? AND taken_at <= ? ORDER BY id ASC
    ```
  - Permute the asset IDs slice in Go memory using Fisher-Yates with a deterministic PRNG seeded by the round seed.
  - Eliminates the need for external SQLite C-extensions or custom functions.
- **Straddle Stitching & Intra-Day Deduplication**:
  - When $\text{offset}_1 + K \le N$: Single-round slice:
    $$\text{part}_1 = \text{deck}_1[\text{offset}_1 : \text{offset}_1 + K]$$
  - When $\text{offset}_1 + K > N$: Straddle slice:
    - $\text{part}_1 = \text{deck}_1[\text{offset}_1 : N]$ (exhausts Round 1).
    - Determine $\text{round}_2 = \text{round}_1 + 1$ and permute $\text{deck}_2$ with $\text{Seed}(\text{round}_2)$.
    - Take the first $K - (N - \text{offset}_1)$ items from $\text{deck}_2$, skipping any IDs that already exist in $\text{part}_1$.
    - Return concatenated slice $\text{part}_1 + \text{part}_2$ (length exactly $K$).
- **Indexed Full-Row Hydration**:
  - Fetch full asset metadata using an indexed `WHERE id IN (...)` query.
  - Re-order results to match the exact sequence of the selected target IDs.
- **Under-Fill Handling**:
  - When $N \le K$, permute all $N$ assets using a daily seed $\text{Seed}(D)$ and return all $N$ assets.

## Testing Decisions

- **Primary Seam**: External HTTP API endpoint `POST /api/search/random` providing `takenAfter`, `takenBefore`, and `size`.
- **Secondary Seam**: Store engine interface `SearchRandomAt(ctx, takenAfter, takenBefore, size, dayOfYear)` enabling deterministic simulation of any day $D$ and multi-round transitions.
- **Test Behaviors**:
  1. *Intra-Round Disjointness*: Days within the same round yield 100% non-overlapping photos.
  2. *Round-Over-Round Reshuffling*: Round $R+1$ returns photos in a completely different sequence and batch grouping than Round $R$.
  3. *Seamless Straddle Stitch*: Verify that on boundary days (e.g. $N=150, K=100$ on Day 2), exactly $K=100$ unique photos are returned (50 from Round 0 + 50 from Round 1).
  4. *Clean Pick-up*: Day 3 picks up at offset 50 of Round 1, delivering 100 photos without skipping.
  5. *Under-Fill Dynamics*: Verify a 35-item library queried with size=100 returns 35 items every day, with each day having a different random sequence.
  6. *Year-Seed Variation*: Calling with year 2026 vs 2027 produces different permutations for the same round.
  7. *Zero DB Writes*: Confirm no SQLite write transactions or `UPDATE` statements occur.
- **Prior Art**: Integration tests in `server_test.go` and `store_test.go`.

## Out of Scope

- Applying multi-round permutation to unbounded random queries (`takenAfter == nil && takenBefore == nil`), which remain pure dynamic random.
- Client-side push notifications or state tracking.
- User-facing rating or manual "hide photo" controls.

## Further Notes

- Aligns with Architectural Decision Record **ADR 0007 (Stateless Deterministic Permutation)**.
- Interactive visualization artifact and web page available at:
  - [docs/visualizations/sampling-permutation.html](file:///Users/ying/code/image-sharing/self-host-digital-photo-frame/docs/visualizations/sampling-permutation.html)
