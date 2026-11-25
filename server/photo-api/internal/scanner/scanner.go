package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
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
	_ "golang.org/x/image/webp"

	"photo-api/internal/geo"
	"photo-api/internal/preview"
	"photo-api/internal/store"
)

var supportedImageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".heic": true,
	".heif": true,
}

var supportedVideoExts = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".m4v":  true,
	".webm": true,
}

const (
	ffprobeTimeout             = 15 * time.Second
	DefaultMinVideoSeconds     = 3.0
	DefaultLivePhotoMaxSeconds = 3.5
)

type Options struct {
	MinVideoSeconds     float64
	LivePhotoMaxSeconds float64
}

var (
	reFilenameDate  = regexp.MustCompile(`(?:^|[^0-9])(19\d{2}|20\d{2})[-_]?([01]\d)[-_]?([0-3]\d)(?:[^0-9]|$)`)
	rePathYearMonth = regexp.MustCompile(`(?:^|/)(19\d{2}|20\d{2})[-/_]([01]\d)(?:/|$)`)
	rePathYearOnly  = regexp.MustCompile(`(?:^|/)(19\d{2}|20\d{2})(?:/|$)`)
	rePurelyNumeric = regexp.MustCompile(`^[0-9\-_.\s]+$`)
)

type Stats struct {
	Added      int   `json:"added"`
	Updated    int   `json:"updated"`
	Deleted    int   `json:"deleted"`
	Skipped    int   `json:"skipped"`
	Errors     int   `json:"errors"`
	DurationMs int64 `json:"durationMs"`
}

type probedVideoDuration struct {
	mtime       int64
	size        int64
	durationSec float64
}

type scanCandidate struct {
	fullPath  string
	relSlash  string
	dirSlash  string
	stemLower string
	mtime     int64
	size      int64
	modTime   time.Time
	isVid     bool
}

type Scanner struct {
	libraryDir          string
	geoNamesDir         string
	excludePatterns     []string
	minVideoSeconds     float64
	livePhotoMaxSeconds float64
	store               *store.Store
	cache               *preview.Cache

	mu     sync.Mutex
	probed map[string]probedVideoDuration
}

func New(libraryDir, geoNamesDir string, excludePatterns []string, st *store.Store, cache *preview.Cache, opts ...Options) *Scanner {
	minVid := DefaultMinVideoSeconds
	liveMax := DefaultLivePhotoMaxSeconds
	if len(opts) > 0 {
		if opts[0].MinVideoSeconds > 0 {
			minVid = opts[0].MinVideoSeconds
		}
		if opts[0].LivePhotoMaxSeconds > 0 {
			liveMax = opts[0].LivePhotoMaxSeconds
		}
	}
	return &Scanner{
		libraryDir:          libraryDir,
		geoNamesDir:         geoNamesDir,
		excludePatterns:     excludePatterns,
		minVideoSeconds:     minVid,
		livePhotoMaxSeconds: liveMax,
		store:               st,
		cache:               cache,
		probed:              make(map[string]probedVideoDuration),
	}
}

func (s *Scanner) shouldExcludeVideo(durationSec float64, hasSiblingImage bool) bool {
	if durationSec < s.minVideoSeconds {
		return true
	}
	if hasSiblingImage && durationSec <= s.livePhotoMaxSeconds {
		return true
	}
	return false
}

func fileDirAndLowerStem(relSlash, baseName string) (string, string) {
	dir := filepath.ToSlash(filepath.Dir(relSlash))
	ext := filepath.Ext(baseName)
	stem := strings.ToLower(strings.TrimSuffix(baseName, ext))
	return dir, stem
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

	var candidates []scanCandidate
	imageStems := make(map[string]map[string]bool)

	err = filepath.WalkDir(s.libraryDir, func(path string, d fs.DirEntry, walkErr error) error {
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
			if path == s.libraryDir {
				return nil
			}
			if shouldSkipDir(name, path, s.libraryDir, s.excludePatterns) {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(name, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		isImg := supportedImageExts[ext]
		isVid := supportedVideoExts[ext]
		if !isImg && !isVid {
			return nil
		}

		relPath, err := filepath.Rel(s.libraryDir, path)
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

		dirSlash, stemLower := fileDirAndLowerStem(relSlash, name)
		if isImg {
			stems := imageStems[dirSlash]
			if stems == nil {
				stems = make(map[string]bool)
				imageStems[dirSlash] = stems
			}
			stems[stemLower] = true
		}

		candidates = append(candidates, scanCandidate{
			fullPath:  path,
			relSlash:  relSlash,
			dirSlash:  dirSlash,
			stemLower: stemLower,
			mtime:     info.ModTime().Unix(),
			size:      info.Size(),
			modTime:   info.ModTime(),
			isVid:     isVid,
		})
		return nil
	})
	if err != nil {
		return stats, err
	}

	nextProbed := make(map[string]probedVideoDuration)

	for _, c := range candidates {
		if ctx.Err() != nil {
			return stats, ctx.Err()
		}

		prev, inExisting := existing[c.relSlash]
		var asset store.Asset

		if !c.isVid {
			seen[c.relSlash] = true
			if inExisting && prev.Mtime == c.mtime && prev.Size == c.size {
				stats.Skipped++
				continue
			}
			asset = buildAsset(c.fullPath, c.relSlash, c.mtime, c.size, c.modTime, loadGeo)
		} else {
			hasSiblingImage := imageStems[c.dirSlash][c.stemLower]
			if inExisting && prev.Mtime == c.mtime && prev.Size == c.size && prev.DurationSec > 0 {
				nextProbed[c.relSlash] = probedVideoDuration{
					mtime:       c.mtime,
					size:        c.size,
					durationSec: prev.DurationSec,
				}
				if s.shouldExcludeVideo(prev.DurationSec, hasSiblingImage) {
					// Leave unseen so the newly excluded video is pruned from SQLite and evicted from cache below.
					continue
				}
				seen[c.relSlash] = true
				stats.Skipped++
				continue
			}

			if !inExisting {
				if memo, ok := s.probed[c.relSlash]; ok && memo.mtime == c.mtime && memo.size == c.size {
					if s.shouldExcludeVideo(memo.durationSec, hasSiblingImage) {
						nextProbed[c.relSlash] = memo
						continue
					}
				}
			}

			meta, probeErr := probeVideo(ctx, c.fullPath)
			if probeErr != nil {
				slog.Warn("ffprobe video asset failed", "path", c.relSlash, "error", probeErr)
				stats.Errors++
				if inExisting {
					seen[c.relSlash] = true
				}
				continue
			}

			nextProbed[c.relSlash] = probedVideoDuration{
				mtime:       c.mtime,
				size:        c.size,
				durationSec: meta.durationSec,
			}
			if s.shouldExcludeVideo(meta.durationSec, hasSiblingImage) {
				continue
			}

			seen[c.relSlash] = true
			asset = buildVideoAssetFromMeta(meta, c.relSlash, c.mtime, c.size, c.modTime, loadGeo)
		}

		if err := s.store.UpsertAsset(ctx, asset); err != nil {
			slog.Warn("upsert asset failed", "path", c.relSlash, "error", err)
			stats.Errors++
			continue
		}
		if inExisting {
			stats.Updated++
			if s.cache != nil {
				s.cache.Evict(prev.ID)
			}
		} else {
			stats.Added++
		}
	}

	s.probed = nextProbed

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

	stats.DurationMs = time.Since(start).Milliseconds()
	slog.Info("scan completed",
		"added", stats.Added,
		"updated", stats.Updated,
		"deleted", stats.Deleted,
		"skipped", stats.Skipped,
		"errors", stats.Errors,
		"duration_ms", stats.DurationMs,
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
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func buildAsset(fullPath, relSlash string, mtime, size int64, modTime time.Time, loadGeo func() *geo.Resolver) store.Asset {
	a := store.Asset{
		ID:          DeterministicUUID(relSlash),
		RelPath:     relSlash,
		MediaType:   "IMAGE",
		Mtime:       mtime,
		Size:        size,
		Album:       deriveAlbum(relSlash),
		Orientation: 1,
		IndexedAt:   time.Now().UTC(),
	}

	if w, h := readImageDimensions(fullPath); w > 0 && h > 0 {
		a.Width = w
		a.Height = h
	}

	var exifDate time.Time
	if x := decodeEXIF(fullPath); x != nil {
		exifDate = extractEXIFDate(x)
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

	a.TakenAt = resolveDate(exifDate, relSlash, modTime)
	a.TakenYear = a.TakenAt.UTC().Year()
	a.TakenMonth = int(a.TakenAt.UTC().Month())
	a.TakenDay = a.TakenAt.UTC().Day()
	return a
}

func readImageDimensions(path string) (int, int) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".heic" || ext == ".heif" {
		return 0, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// decodeEXIF decodes EXIF from JPEG/TIFF directly, and for HEIC/HEIF locates
// the embedded "Exif\x00\x00" TIFF payload in the container header and wraps
// it in a minimal JPEG APP1 envelope for goexif.
func decodeEXIF(path string) *exif.Exif {
	ext := strings.ToLower(filepath.Ext(path))
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if ext == ".heic" || ext == ".heif" {
		return decodeHEICEXIF(f)
	}
	x, err := exif.Decode(f)
	if err != nil {
		return nil
	}
	return x
}

func decodeHEICEXIF(r io.Reader) *exif.Exif {
	// Read up to 512KB of header/metadata bytes where HEIC stores the Exif item.
	data, err := io.ReadAll(io.LimitReader(r, 512*1024))
	if err != nil {
		return nil
	}
	marker := []byte("Exif\x00\x00")
	idx := bytes.Index(data, marker)
	if idx < 0 {
		return nil
	}
	payload := data[idx:]
	// Cap APP1 segment length to 65533 bytes (max JPEG APP1 payload)
	if len(payload) > 65533 {
		payload = payload[:65533]
	}
	var jpegBuf bytes.Buffer
	jpegBuf.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	_ = binary.Write(&jpegBuf, binary.BigEndian, uint16(len(payload)+2))
	jpegBuf.Write(payload)
	jpegBuf.Write([]byte{0xFF, 0xD9})

	x, err := exif.Decode(&jpegBuf)
	if err != nil {
		return nil
	}
	return x
}

// extractEXIFDate checks DateTimeOriginal -> DateTimeDigitized (CreateDate) -> DateTime.
func extractEXIFDate(x *exif.Exif) time.Time {
	for _, field := range []exif.FieldName{exif.DateTimeOriginal, exif.DateTimeDigitized, exif.DateTime} {
		tag, err := x.Get(field)
		if err != nil {
			continue
		}
		raw, err := tag.StringVal()
		if err != nil {
			continue
		}
		raw = strings.TrimSpace(strings.TrimRight(raw, "\x00"))
		if t, err := time.Parse("2006:01:02 15:04:05", raw); err == nil && !t.IsZero() {
			return t.UTC()
		}
	}
	return time.Time{}
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
