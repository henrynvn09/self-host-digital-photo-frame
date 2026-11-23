package store

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const assetColumns = `id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
       city, state, country, album, orientation, width, height, indexed_at, dealt_at`

type Asset struct {
	ID          string
	RelPath     string
	Mtime       int64
	Size        int64
	TakenAt     time.Time
	TakenYear   int
	TakenMonth  int
	TakenDay    int
	City        string
	State       string
	Country     string
	Album       string
	Orientation int
	Width       int
	Height      int
	IndexedAt   time.Time
	DealtAt     *time.Time
}

type PathMeta struct {
	ID    string
	Mtime int64
	Size  int64
}

type SearchParams struct {
	TakenAfter  *time.Time
	TakenBefore *time.Time
	Order       string // "asc" or "desc"
	Page        int
	Size        int
}

type SearchResult struct {
	Items []Asset
	Total int
}

// NormalizePageSize clamps pagination parameters consistently across API and Store.
func NormalizePageSize(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 250
	}
	if size > 1000 {
		size = 1000
	}
	return page, size
}

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	dbPath := filepath.Join(dataDir, "index.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS assets (
	id TEXT PRIMARY KEY,
	rel_path TEXT NOT NULL UNIQUE,
	mtime INTEGER NOT NULL,
	size INTEGER NOT NULL,
	taken_at TEXT NOT NULL,
	taken_year INTEGER NOT NULL,
	taken_month INTEGER NOT NULL,
	taken_day INTEGER NOT NULL,
	city TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	country TEXT NOT NULL DEFAULT '',
	album TEXT NOT NULL DEFAULT '',
	orientation INTEGER NOT NULL DEFAULT 1,
	width INTEGER NOT NULL DEFAULT 0,
	height INTEGER NOT NULL DEFAULT 0,
	indexed_at TEXT NOT NULL,
	dealt_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_assets_taken_at ON assets(taken_at);
CREATE INDEX IF NOT EXISTS idx_assets_month_day ON assets(taken_month, taken_day, taken_year);
CREATE INDEX IF NOT EXISTS idx_assets_rel_path ON assets(rel_path);
`
	_, err := s.db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("migrate sqlite schema: %w", err)
	}

	// For pre-existing databases without dealt_at column
	_, _ = s.db.Exec(`ALTER TABLE assets ADD COLUMN dealt_at INTEGER`)
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_assets_taken_dealt ON assets(taken_at, dealt_at)`); err != nil {
		return fmt.Errorf("create idx_assets_taken_dealt: %w", err)
	}
	return nil
}

func (s *Store) ListPathMeta(ctx context.Context) (map[string]PathMeta, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rel_path, id, mtime, size FROM assets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]PathMeta)
	for rows.Next() {
		var relPath string
		var pm PathMeta
		if err := rows.Scan(&relPath, &pm.ID, &pm.Mtime, &pm.Size); err != nil {
			return nil, err
		}
		out[relPath] = pm
	}
	return out, rows.Err()
}

func (s *Store) UpsertAsset(ctx context.Context, a Asset) error {
	const q = `
INSERT INTO assets (
	id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
	city, state, country, album, orientation, width, height, indexed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(rel_path) DO UPDATE SET
	id = excluded.id,
	mtime = excluded.mtime,
	size = excluded.size,
	taken_at = excluded.taken_at,
	taken_year = excluded.taken_year,
	taken_month = excluded.taken_month,
	taken_day = excluded.taken_day,
	city = excluded.city,
	state = excluded.state,
	country = excluded.country,
	album = excluded.album,
	orientation = excluded.orientation,
	width = excluded.width,
	height = excluded.height,
	indexed_at = excluded.indexed_at
`
	takenUTC := a.TakenAt.UTC()
	_, err := s.db.ExecContext(ctx, q,
		a.ID,
		a.RelPath,
		a.Mtime,
		a.Size,
		takenUTC.Format(time.RFC3339),
		takenUTC.Year(),
		int(takenUTC.Month()),
		takenUTC.Day(),
		a.City,
		a.State,
		a.Country,
		a.Album,
		a.Orientation,
		a.Width,
		a.Height,
		a.IndexedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) DeleteByRelPaths(ctx context.Context, relPaths []string) error {
	if len(relPaths) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `DELETE FROM assets WHERE rel_path = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range relPaths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetAsset(ctx context.Context, id string) (*Asset, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = ?`, id)
	a, err := scanAsset(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) SearchMetadata(ctx context.Context, p SearchParams) (SearchResult, error) {
	where, args := buildDateWhere(p.TakenAfter, p.TakenBefore)

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets`+where, args...).Scan(&total); err != nil {
		return SearchResult{}, err
	}

	page, size := NormalizePageSize(p.Page, p.Size)
	offset := (page - 1) * size

	orderDir := "DESC"
	if strings.EqualFold(p.Order, "asc") {
		orderDir = "ASC"
	}

	q := fmt.Sprintf(`SELECT %s FROM assets%s ORDER BY taken_at %s LIMIT ? OFFSET ?`, assetColumns, where, orderDir)
	qArgs := append(append([]any{}, args...), size, offset)
	rows, err := s.db.QueryContext(ctx, q, qArgs...)
	if err != nil {
		return SearchResult{}, err
	}
	defer rows.Close()

	items, err := scanAssets(rows)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Items: items, Total: total}, nil
}

const (
	defaultRandomSize = 100
	maxRandomSize     = 1000
)

// SearchRandom returns a deterministic pseudo-random slice of assets for the current day.
func (s *Store) SearchRandom(ctx context.Context, takenAfter, takenBefore *time.Time, size int) ([]Asset, error) {
	now := time.Now().UTC()
	return s.SearchRandomAtYear(ctx, takenAfter, takenBefore, size, now.YearDay(), now.Year())
}

// SearchRandomAt performs deterministic multi-round pseudorandom sampling without replacement based on dayOfYear.
// It uses the current calendar year to salt the seed.
func (s *Store) SearchRandomAt(ctx context.Context, takenAfter, takenBefore *time.Time, size int, dayOfYear int) ([]Asset, error) {
	return s.SearchRandomAtYear(ctx, takenAfter, takenBefore, size, dayOfYear, time.Now().UTC().Year())
}

// SearchRandomAtYear performs deterministic multi-round pseudorandom sampling without replacement with seamless straddle stitch.
// It requires zero database writes, eliminates lock contention, and provides idempotent daily pools.
func (s *Store) SearchRandomAtYear(ctx context.Context, takenAfter, takenBefore *time.Time, size int, dayOfYear int, currentYear int) ([]Asset, error) {
	if size <= 0 {
		size = defaultRandomSize
	}
	if size > maxRandomSize {
		size = maxRandomSize
	}

	// If no date bounds are specified, maintain pure stateless random sampling across the entire library
	if takenAfter == nil && takenBefore == nil {
		q := fmt.Sprintf(`SELECT %s FROM assets ORDER BY RANDOM() LIMIT ?`, assetColumns)
		rows, err := s.db.QueryContext(ctx, q, size)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanAssets(rows)
	}

	where, args := buildDateWhere(takenAfter, takenBefore)

	allIDs, err := s.listMatchingIDs(ctx, where, args)
	if err != nil {
		return nil, err
	}
	total := len(allIDs)
	if total == 0 {
		return []Asset{}, nil
	}

	if dayOfYear <= 0 {
		dayOfYear = 1
	}

	// Under-fill: If total matching assets in this window <= requested size,
	// return all assets permuted with a daily seed so order is never frozen/static
	if total <= size {
		deck := permuteIDs(allIDs, currentYear, takenAfter, takenBefore, dayOfYear-1)
		return s.getAssetsByIDs(ctx, deck)
	}

	// Multi-Round Virtual Concatenated Tape with Seamless Straddle Stitch:
	globalStart := (dayOfYear - 1) * size
	round1 := globalStart / total
	offset1 := globalStart % total

	deck1 := getRoundDeck(allIDs, currentYear, takenAfter, takenBefore, size, round1)

	var targetIDs []string
	if offset1+size <= total {
		// Single-round slice: completely contained in round1
		targetIDs = deck1[offset1 : offset1+size]
	} else {
		// Seamless Straddle Stitch:
		// Part 1: take remaining items from round1 (exhausts round1 cleanly)
		part1 := deck1[offset1:]

		// Part 2: advance to round2 = round1 + 1 (freshly shuffled with round2 seed)
		round2 := round1 + 1
		deck2 := getRoundDeck(allIDs, currentYear, takenAfter, takenBefore, size, round2)
		needed := size - len(part1)
		part2 := deck2[:needed]

		targetIDs = make([]string, 0, size)
		targetIDs = append(targetIDs, part1...)
		targetIDs = append(targetIDs, part2...)
	}

	return s.getAssetsByIDs(ctx, targetIDs)
}

func (s *Store) listMatchingIDs(ctx context.Context, where string, args []any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM assets`+where+` ORDER BY id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var allIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allIDs = append(allIDs, id)
	}
	return allIDs, rows.Err()
}

func getRoundDeck(allIDs []string, currentYear int, takenAfter, takenBefore *time.Time, size int, targetRound int) []string {
	total := len(allIDs)
	if total <= size || targetRound == 0 {
		return permuteIDs(allIDs, currentYear, takenAfter, takenBefore, targetRound)
	}

	stepGcd := gcd(total, size)
	period := size / stepGcd
	startRound := targetRound - (targetRound % period)

	currentDeck := permuteIDs(allIDs, currentYear, takenAfter, takenBefore, startRound)
	if startRound == targetRound {
		return currentDeck
	}

	for r := startRound + 1; r <= targetRound; r++ {
		tailCount := (r * total) % size
		nextDeck := permuteIDs(allIDs, currentYear, takenAfter, takenBefore, r)
		if tailCount == 0 {
			currentDeck = nextDeck
			continue
		}

		tailItems := make(map[string]bool, tailCount)
		for _, id := range currentDeck[total-tailCount:] {
			tailItems[id] = true
		}

		headNeeded := size - tailCount
		j := headNeeded
		for i := 0; i < headNeeded; i++ {
			if tailItems[nextDeck[i]] {
				for j < total && tailItems[nextDeck[j]] {
					j++
				}
				if j < total {
					nextDeck[i], nextDeck[j] = nextDeck[j], nextDeck[i]
					j++
				}
			}
		}
		currentDeck = nextDeck
	}

	return currentDeck
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func permuteIDs(ids []string, currentYear int, takenAfter, takenBefore *time.Time, round int) []string {
	deck := make([]string, len(ids))
	copy(deck, ids)

	var afterStr, beforeStr string
	if takenAfter != nil {
		afterStr = takenAfter.UTC().Format(time.RFC3339)
	}
	if takenBefore != nil {
		beforeStr = takenBefore.UTC().Format(time.RFC3339)
	}

	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d|%s|%s|%d", currentYear, afterStr, beforeStr, round)
	seed := int64(h.Sum64())

	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(deck), func(i, j int) {
		deck[i], deck[j] = deck[j], deck[i]
	})
	return deck
}

func (s *Store) getAssetsByIDs(ctx context.Context, ids []string) ([]Asset, error) {
	if len(ids) == 0 {
		return []Asset{}, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	q := fmt.Sprintf(`SELECT %s FROM assets WHERE id IN (%s)`, assetColumns, strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assets, err := scanAssets(rows)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]Asset, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}

	ordered := make([]Asset, 0, len(ids))
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			ordered = append(ordered, a)
		}
	}
	return ordered, nil
}

// ResetDealt clears dealt timestamps for assets within the specified date window (or all assets if nil).
func (s *Store) ResetDealt(ctx context.Context, takenAfter, takenBefore *time.Time) error {
	where, args := buildDateWhere(takenAfter, takenBefore)
	q := `UPDATE assets SET dealt_at = NULL` + where
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

func (s *Store) MemoriesForDate(ctx context.Context, month, day, currentYear int) ([]Asset, error) {
	q := fmt.Sprintf(`SELECT %s FROM assets WHERE taken_month = ? AND taken_day = ? AND taken_year < ? ORDER BY taken_year DESC, taken_at ASC`, assetColumns)
	rows, err := s.db.QueryContext(ctx, q, month, day, currentYear)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

func buildDateWhere(takenAfter, takenBefore *time.Time) (string, []any) {
	var clauses []string
	var args []any
	if takenAfter != nil {
		clauses = append(clauses, "taken_at >= ?")
		args = append(args, takenAfter.UTC().Format(time.RFC3339))
	}
	if takenBefore != nil {
		clauses = append(clauses, "taken_at <= ?")
		args = append(args, takenBefore.UTC().Format(time.RFC3339))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAsset(r rowScanner) (Asset, error) {
	var a Asset
	var takenStr, indexedStr string
	var dealtAtVal sql.NullInt64
	err := r.Scan(
		&a.ID, &a.RelPath, &a.Mtime, &a.Size,
		&takenStr, &a.TakenYear, &a.TakenMonth, &a.TakenDay,
		&a.City, &a.State, &a.Country, &a.Album,
		&a.Orientation, &a.Width, &a.Height, &indexedStr,
		&dealtAtVal,
	)
	if err != nil {
		return Asset{}, err
	}
	a.TakenAt, _ = time.Parse(time.RFC3339, takenStr)
	a.IndexedAt, _ = time.Parse(time.RFC3339, indexedStr)
	if dealtAtVal.Valid {
		t := time.Unix(dealtAtVal.Int64, 0).UTC()
		a.DealtAt = &t
	}
	return a, nil
}

func scanAssets(rows *sql.Rows) ([]Asset, error) {
	var out []Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
