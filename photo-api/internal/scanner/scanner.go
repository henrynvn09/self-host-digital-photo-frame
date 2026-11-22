package scanner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rwcarlsen/goexif/exif"

	"photo-api/internal/geo"
	"photo-api/internal/preview"
	"photo-api/internal/store"
)

var supportedExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".heic": true,
	".heif": true,
}

var (
	reFilenameDate   = regexp.MustCompile(`(?:^|[^0-9])(19\d{2}|20\d{2})[-_]?([01]\d)[-_]?([0-3]\d)(?:[^0-9]|$)`)
	rePathYearMonth  = regexp.MustCompile(`(?:^|/)(19\d{2}|20\d{2})[-/_]([01]\d)(?:/|$)`)
	rePathYearOnly   = regexp.MustCompile(`(?:^|/)(19\d{2}|20\d{2})(?:/|$)`)
	rePurelyNumeric  = regexp.MustCompile(`^[0-9\-_.\s]+$`)
)

type Stats struct {
	Added    int           `json:"added"`
	Updated  int           `json:"updated"`
	Deleted  int           `json:"deleted"`
	Skipped  int           `json:"skipped"`
	Errors   int           `json:"errors"`
	Duration time.Duration `json:"durationMs"`
}

type Scanner struct {
	photosDir       string
	geoNamesDir     string
	excludePatterns []string
	store           *store.Store
	cache           *preview.Cache

	mu sync.Mutex
}

func New(photosDir, geoNamesDir string, excludePatterns []string, st *store.Store, cache *preview.Cache) *Scanner {
	return &Scanner{
		photosDir:       photosDir,
		geoNamesDir:     geoNamesDir,
		excludePatterns: excludePatterns,
		store:           st,
		cache:           cache,
	}
}

func (s *Scanner) Scan(ctx context.Context) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()
	var stats Stats

	existing, err := s.store.ListPathMeta(ctx)
	if err != nil {
		return stats, fmt.Errorf("list existing assets: %w", err)
	}

	seen := make(map[string]bool, len(existing))
	var geoResolver *geo.Resolver
	geoLoaded := false

	loadGeo := func() *geo.Resolver {
		if !geoLoaded {
			geoLoaded = true
			r, err := geo.LoadFromDir(s.geoNamesDir)
			if err != nil {
				slog.Warn("failed to load geonames", "dir", s.geoNamesDir, "error", err)
			} else if r != nil {
				geoResolver = r
			}
		}
		return geoResolver
	}

	err = filepath.WalkDir(s.photosDir, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			slog.Warn("walk error", "path", path, "error", walkErr)
			stats.Errors++
			return nil
		}

		name := d.Name()
		if d.IsDir() {
			if path == s.photosDir {
				return nil
			}
			if shouldSkipDir(name, path, s.photosDir, s.excludePatterns) {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !supportedExts[ext] {
			return nil
		}

		relPath, err := filepath.Rel(s.photosDir, path)
		if err != nil {
			stats.Errors++
			return nil
		}
		relSlash := filepath.ToSlash(relPath)
		if matchesAnyExclude(relSlash, name, s.excludePatterns) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			stats.Errors++
			return nil
		}

		seen[relSlash] = true
		mtime := info.ModTime().Unix()
		size := info.Size()

		if prev, ok := existing[relSlash]; ok && prev.Mtime == mtime && prev.Size == size {
			stats.Skipped++
			return nil
		}

		asset := buildAsset(path, relSlash, mtime, size, info.ModTime(), loadGeo)
		if err := s.store.UpsertAsset(ctx, asset); err != nil {
			slog.Warn("upsert asset failed", "path", relSlash, "error", err)
			stats.Errors++
			return nil
		}

		if prev, ok := existing[relSlash]; ok {
			stats.Updated++
			if s.cache != nil {
				s.cache.Evict(prev.ID)
			}
		} else {
			stats.Added++
		}
		return nil
	})
	if err != nil {
		return stats, err
	}

	var toDelete []string
	for relPath, meta := range existing {
		if !seen[relPath] {
			toDelete = append(toDelete, relPath)
			if s.cache != nil {
				s.cache.Evict(meta.ID)
			}
		}
	}
	if len(toDelete) > 0 {
		if err := s.store.DeleteByRelPaths(ctx, toDelete); err != nil {
			return stats, fmt.Errorf("delete removed assets: %w", err)
		}
		stats.Deleted = len(toDelete)
	}

	// Release GeoNames in-memory index after scan so idle NAS RAM stays minimal.
	if geoResolver != nil {
		geoResolver = nil
		runtime.GC()
	}

	stats.Duration = time.Since(start)
	slog.Info("scan completed",
		"added", stats.Added,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration", stats.Duration.Round(time.Millisecond),
	)
	return stats, nil
}

func shouldSkipDir(name, fullPath, rootDir string, patterns []string) bool {
	if name == "@eaDir" || name == "#recycle" || strings.HasPrefix(name, ".") {
		return true
	}
	rel, err := filepath.Rel(rootDir, fullPath)
	if err != nil {
		return false
	}
	return matchesAnyExclude(filepath.ToSlash(rel), name, patterns)
}

func matchesAnyExclude(relSlash, baseName string, patterns []string) bool {
	for _, pat := range patterns {
		if ok, _ := filepath.Match(pat, baseName); ok {
			return true
		}
		if ok, _ := filepath.Match(pat, relSlash); ok {
			return true
		}
		if strings.Contains(relSlash, pat) && !strings.ContainsAny(pat, "*?[") {
			return true
		}
	}
	return false
}

// DeterministicUUID produces a stable UUID-formatted ID from a relative path.
func DeterministicUUID(relPath string) string {
	sum := sha256.Sum256([]byte(relPath))
	// Set RFC 4122 version 5 & variant bits for standard UUID format
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func buildAsset(fullPath, relSlash string, mtime, size int64, modTime time.Time, loadGeo func() *geo.Resolver) store.Asset {
	a := store.Asset{
		ID:          DeterministicUUID(relSlash),
		RelPath:     relSlash,
		Mtime:       mtime,
		Size:        size,
		Album:       deriveAlbum(relSlash),
		Orientation: 1,
		IndexedAt:   time.Now().UTC(),
	}

	var exifDate time.Time
	if f, err := os.Open(fullPath); err == nil {
		if x, err := exif.Decode(f); err == nil {
			if dt, err := x.DateTime(); err == nil && !dt.IsZero() {
				exifDate = dt
			}
			if tag, err := x.Get(exif.Orientation); err == nil {
				if v, err := tag.Int(0); err == nil && v >= 1 && v <= 8 {
					a.Orientation = v
				}
			}
			if lat, lon, err := x.LatLong(); err == nil && (lat != 0 || lon != 0) {
				if r := loadGeo(); r != nil {
					if loc, ok := r.Lookup(lat, lon); ok {
						a.City = loc.City
						a.State = loc.State
						a.Country = loc.Country
					}
				}
			}
		}
		_ = f.Close()
	}

	a.TakenAt = resolveDate(exifDate, relSlash, modTime)
	a.TakenYear = a.TakenAt.UTC().Year()
	a.TakenMonth = int(a.TakenAt.UTC().Month())
	a.TakenDay = a.TakenAt.UTC().Day()
	return a
}

// resolveDate enforces precedence: EXIF -> filename date -> folder path date -> file mtime.
func resolveDate(exifDate time.Time, relSlash string, modTime time.Time) time.Time {
	if !exifDate.IsZero() && exifDate.Year() >= 1970 {
		return exifDate.UTC()
	}
	base := filepath.Base(relSlash)
	if m := reFilenameDate.FindStringSubmatch(base); len(m) == 4 {
		if t, ok := parseYMD(m[1], m[2], m[3]); ok {
			return t
		}
	}
	dir := filepath.Dir(relSlash)
	if dir != "." {
		if m := rePathYearMonth.FindStringSubmatch(dir); len(m) == 3 {
			if t, ok := parseYMD(m[1], m[2], "01"); ok {
				return t
			}
		}
		if m := rePathYearOnly.FindStringSubmatch(dir); len(m) == 2 {
			if t, ok := parseYMD(m[1], "01", "01"); ok {
				return t
			}
		}
	}
	return modTime.UTC()
}

func parseYMD(yStr, mStr, dStr string) (time.Time, bool) {
	y, err1 := strconv.Atoi(yStr)
	m, err2 := strconv.Atoi(mStr)
	d, err3 := strconv.Atoi(dStr)
	if err1 != nil || err2 != nil || err3 != nil {
		return time.Time{}, false
	}
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 12, 0, 0, 0, time.UTC)
	if int(t.Month()) != m || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

// deriveAlbum returns the immediate parent directory name, omitted when at root
// or purely numeric (e.g. "2023", "07", "2023-07").
func deriveAlbum(relSlash string) string {
	dir := filepath.Dir(relSlash)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	parent := filepath.Base(dir)
	if parent == "." || parent == "/" || rePurelyNumeric.MatchString(parent) {
		return ""
	}
	return parent
}
