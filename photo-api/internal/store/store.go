package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

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
	indexed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_assets_taken_at ON assets(taken_at);
CREATE INDEX IF NOT EXISTS idx_assets_month_day ON assets(taken_month, taken_day, taken_year);
CREATE INDEX IF NOT EXISTS idx_assets_rel_path ON assets(rel_path);
`
	_, err := s.db.Exec(ddl)
	if err != nil {
		return fmt.Errorf("migrate sqlite schema: %w", err)
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
	row := s.db.QueryRowContext(ctx, `
SELECT id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
       city, state, country, album, orientation, width, height, indexed_at
FROM assets WHERE id = ?`, id)
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

	size := p.Size
	if size <= 0 {
		size = 250
	}
	if size > 1000 {
		size = 1000
	}
	page := p.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * size

	orderDir := "DESC"
	if strings.EqualFold(p.Order, "asc") {
		orderDir = "ASC"
	}

	q := fmt.Sprintf(`
SELECT id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
       city, state, country, album, orientation, width, height, indexed_at
FROM assets%s
ORDER BY taken_at %s
LIMIT ? OFFSET ?`, where, orderDir)

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

func (s *Store) SearchRandom(ctx context.Context, takenAfter, takenBefore *time.Time, size int) ([]Asset, error) {
	where, args := buildDateWhere(takenAfter, takenBefore)
	if size <= 0 {
		size = 100
	}
	if size > 1000 {
		size = 1000
	}

	q := fmt.Sprintf(`
SELECT id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
       city, state, country, album, orientation, width, height, indexed_at
FROM assets%s
ORDER BY RANDOM()
LIMIT ?`, where)

	qArgs := append(append([]any{}, args...), size)
	rows, err := s.db.QueryContext(ctx, q, qArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssets(rows)
}

func (s *Store) MemoriesForDate(ctx context.Context, month, day, currentYear int) ([]Asset, error) {
	const q = `
SELECT id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
       city, state, country, album, orientation, width, height, indexed_at
FROM assets
WHERE taken_month = ? AND taken_day = ? AND taken_year < ?
ORDER BY taken_year DESC, taken_at ASC`

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
	err := r.Scan(
		&a.ID, &a.RelPath, &a.Mtime, &a.Size,
		&takenStr, &a.TakenYear, &a.TakenMonth, &a.TakenDay,
		&a.City, &a.State, &a.Country, &a.Album,
		&a.Orientation, &a.Width, &a.Height, &indexedStr,
	)
	if err != nil {
		return Asset{}, err
	}
	a.TakenAt, _ = time.Parse(time.RFC3339, takenStr)
	a.IndexedAt, _ = time.Parse(time.RFC3339, indexedStr)
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
