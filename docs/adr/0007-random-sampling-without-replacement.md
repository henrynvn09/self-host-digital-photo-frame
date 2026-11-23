# 0007. Stateless Deterministic Permutation for Anniversary Random Sampling

Date: 2026-11-23

## Status

Accepted (Supersedes stateful DB tracking)

## Context

The digital photo frame's primary mode is `anniversary` mode, which queries `photo-api` once a day for up to 100 photos per year across 2010–2026 within a seasonal window ($\pm45$ days around current date).

Previously, queries executed naive stateless random sampling (`ORDER BY RANDOM() LIMIT 100`). In large photo libraries (e.g. 10k+ photos) or smaller yearly subsets (e.g. 150 photos), pure random sampling with replacement often picked recently displayed photos multiple days in a row while leaving hundreds of other anniversary photos unviewed.

An initial proposal evaluated stateful tracking via a database column (`dealt_at`) and write-heavy `UPDATE` cycles. However, adding database writes and lock contention to high-frequency slideshow queries introduces unnecessary write churn, database locking, and multi-client state coupling.

## Decision

1. **Deterministic Pseudorandom Permutation via Natural UUIDv5 Shuffling**:
   - Asset IDs are deterministic UUIDv5 hashes derived from relative file paths.
   - Because cryptographic hash functions yield uniformly distributed bit patterns, ordering rows by `id ASC` naturally produces a cryptographically uniform pseudorandom permutation of the entire photo library.

2. **Stateless Cyclic Day-of-Year Offset**:
   - For an anniversary window with $N$ matching assets and requested page size $K$, the daily starting offset is computed directly from the calendar day of the year ($D \in [1..366]$):
     $$\text{DailyOffset} = ((D - 1) \times K) \pmod N$$
   - Consecutive days ($D, D+1$) advance by exactly $K$ positions, guaranteeing disjoint non-overlapping slices until all $N$ assets have been exhausted.

3. **Under-Fill and Wraparound Guarantees**:
   - **Under-fill**: If $N \le K$, the query returns all $N$ matching assets ordered by `id ASC` without artificial duplication.
   - **Wraparound**: When $\text{offset} + K > N$, the query seamlessly splices the remainder of the array ($\text{items}[\text{offset}..N-1]$) with the start of the array ($\text{items}[0..K - (N - \text{offset}) - 1]$), ensuring full $K$ items are returned without duplicates within any single day.

4. **Zero Database Writes & Pure Read Operations**:
   - Slideshow and anniversary queries execute purely as `SELECT COUNT(*)` followed by `SELECT ... ORDER BY id ASC LIMIT ? OFFSET ?`.
   - SQLite write transactions, WAL file growth, and database lock contention are completely eliminated during slideshow playback.
   - Multi-device displays (multiple digital photo frames across rooms) naturally display consistent, synchronized photo pools for each day.

5. **Pure Stateless Random Fallback**:
   - For global queries without date bounds (`takenAfter == nil && takenBefore == nil`), `photo-api` continues to use `ORDER BY RANDOM() LIMIT ?`.

## Interactive Visualization

An interactive, self-contained visualizer simulating pool sizing ($N$), request sizing ($K$), multi-round Fisher-Yates shuffles, and tail flush boundaries is available in the repository at:
- [docs/visualizations/sampling-permutation.html](file:///Users/ying/code/image-sharing/self-host-digital-photo-frame/docs/visualizations/sampling-permutation.html)

- **Pros**:
  - **Zero DB Writes**: Pure read-only queries with no disk writes, lock contention, or transaction rollbacks.
  - **Sampling Without Replacement**: Consecutively viewed days yield 100% disjoint assets until the yearly window is fully exhausted.
  - **Idempotency**: Multiple calls or multiple clients on the same day receive the exact same deterministic daily pool.
  - **Zero Client Changes**: Fully backwards-compatible with `MMM-ImmichSlideShow` and existing API contracts (`POST /api/search/random`).
  - **Instant Recovery**: Survives container restarts, crashes, or cache clears with zero state drift or recovery required.
- **Cons**:
  - The sequence of photos for a given day is fixed by calendar day and library contents; adding or removing photos from an anniversary window shifts the cyclic modulo offset gracefully.

