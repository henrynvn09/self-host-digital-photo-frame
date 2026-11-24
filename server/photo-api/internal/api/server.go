package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"photo-api/internal/preview"
	"photo-api/internal/scanner"
	"photo-api/internal/store"
)

type Server struct {
	libraryDir string
	apiKey     string
	store      *store.Store
	scanner    *scanner.Scanner
	cache      *preview.Cache
	mux        *http.ServeMux
}

func NewServer(libraryDir, apiKey string, st *store.Store, sc *scanner.Scanner, cache *preview.Cache) *Server {
	s := &Server{
		libraryDir: libraryDir,
		apiKey:     apiKey,
		store:      st,
		scanner:    sc,
		cache:      cache,
		mux:        http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.withAPIKey(s.mux)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/server/ping", s.handlePing)
	s.mux.HandleFunc("GET /api/server/version", s.handleVersion)
	s.mux.HandleFunc("POST /api/search/metadata", s.handleSearchMetadata)
	s.mux.HandleFunc("POST /api/search/smart", s.handleSearchMetadata) // Aliased: ignores ML query text
	s.mux.HandleFunc("POST /api/search/random", s.handleSearchRandom)
	s.mux.HandleFunc("GET /api/memories", s.handleMemories)
	s.mux.HandleFunc("GET /api/assets/{id}", s.handleGetAsset)
	s.mux.HandleFunc("GET /api/assets/{id}/thumbnail", s.handleAssetPreview)
	s.mux.HandleFunc("GET /api/assets/{id}/backdrop", s.handleAssetBackdrop)
	s.mux.HandleFunc("GET /api/assets/{id}/stream", s.handleAssetStream)
	s.mux.HandleFunc("POST /api/admin/scan", s.handleAdminScan)
}

func (s *Server) withAPIKey(next http.Handler) http.Handler {
	if s.apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/server/ping" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("x-api-key")
		if key == "" {
			key = r.URL.Query().Get("apiKey")
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.apiKey)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Invalid API key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"res": "pong"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]int{
		"major": 1,
		"minor": 132,
		"patch": 0,
	})
}

type immichExifInfo struct {
	DateTimeOriginal string  `json:"dateTimeOriginal"`
	City             *string `json:"city"`
	State            *string `json:"state"`
	Country          *string `json:"country"`
	Orientation      string  `json:"orientation,omitempty"`
	ExifImageWidth   int     `json:"exifImageWidth,omitempty"`
	ExifImageHeight  int     `json:"exifImageHeight,omitempty"`
}

type immichAsset struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	DurationSeconds  float64        `json:"durationSeconds"`
	OriginalPath     string         `json:"originalPath"`
	OriginalFileName string         `json:"originalFileName"`
	FileCreatedAt    string         `json:"fileCreatedAt"`
	FileModifiedAt   string         `json:"fileModifiedAt"`
	LocalDateTime    string         `json:"localDateTime"`
	ExifInfo         immichExifInfo `json:"exifInfo"`
	AlbumName        string         `json:"albumName,omitempty"`
}

func toImmichAsset(a store.Asset) immichAsset {
	taken := a.TakenAt.UTC().Format(time.RFC3339)
	mod := time.Unix(a.Mtime, 0).UTC().Format(time.RFC3339)
	assetType := "IMAGE"
	var dur float64
	if strings.EqualFold(a.MediaType, "VIDEO") {
		assetType = "VIDEO"
		dur = a.DurationSec
	}
	return immichAsset{
		ID:               a.ID,
		Type:             assetType,
		DurationSeconds:  dur,
		OriginalPath:     a.RelPath,
		OriginalFileName: filepath.Base(a.RelPath),
		FileCreatedAt:    taken,
		FileModifiedAt:   mod,
		LocalDateTime:    taken,
		ExifInfo: immichExifInfo{
			DateTimeOriginal: taken,
			City:             nullableString(a.City),
			State:            nullableString(a.State),
			Country:          nullableString(a.Country),
			Orientation:      strconv.Itoa(a.Orientation),
			ExifImageWidth:   a.Width,
			ExifImageHeight:  a.Height,
		},
		AlbumName: a.Album,
	}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type searchRequest struct {
	Query       string `json:"query"` // Intentionally ignored (no ML smart search)
	TakenAfter  string `json:"takenAfter"`
	TakenBefore string `json:"takenBefore"`
	Type        string `json:"type"`
	Order       string `json:"order"`
	Page        int    `json:"page"`
	Size        int    `json:"size"`
}

type parsedSearch struct {
	unknownType bool
	mediaType   string
	takenAfter  *time.Time
	takenBefore *time.Time
	order       string
	page        int
	size        int
}

func parseSearchRequest(r *http.Request) (parsedSearch, error) {
	var req searchRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	var mediaType string
	switch strings.ToUpper(strings.TrimSpace(req.Type)) {
	case "", "ALL":
		mediaType = ""
	case "IMAGE":
		mediaType = "IMAGE"
	case "VIDEO":
		mediaType = "VIDEO"
	default:
		return parsedSearch{unknownType: true}, nil
	}
	takenAfter, err := parseOptionalTime(req.TakenAfter)
	if err != nil {
		return parsedSearch{}, fmt.Errorf("invalid takenAfter")
	}
	takenBefore, err := parseOptionalTime(req.TakenBefore)
	if err != nil {
		return parsedSearch{}, fmt.Errorf("invalid takenBefore")
	}
	return parsedSearch{
		mediaType:   mediaType,
		takenAfter:  takenAfter,
		takenBefore: takenBefore,
		order:       req.Order,
		page:        req.Page,
		size:        req.Size,
	}, nil
}

func (s *Server) handleSearchMetadata(w http.ResponseWriter, r *http.Request) {
	ps, err := parseSearchRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	if ps.unknownType {
		writeJSON(w, http.StatusOK, map[string]any{
			"assets": map[string]any{
				"total":    0,
				"count":    0,
				"items":    []immichAsset{},
				"nextPage": nil,
			},
		})
		return
	}

	page, size := store.NormalizePageSize(ps.page, ps.size)
	res, err := s.store.SearchMetadata(r.Context(), store.SearchParams{
		TakenAfter:  ps.takenAfter,
		TakenBefore: ps.takenBefore,
		MediaType:   ps.mediaType,
		Order:       ps.order,
		Page:        page,
		Size:        size,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	items := make([]immichAsset, 0, len(res.Items))
	for _, a := range res.Items {
		items = append(items, toImmichAsset(a))
	}

	var nextPage *string
	if page*size < res.Total {
		np := strconv.Itoa(page + 1)
		nextPage = &np
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"assets": map[string]any{
			"total":    res.Total,
			"count":    len(items),
			"items":    items,
			"nextPage": nextPage,
		},
	})
}

func (s *Server) handleSearchRandom(w http.ResponseWriter, r *http.Request) {
	ps, err := parseSearchRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	if ps.unknownType {
		writeJSON(w, http.StatusOK, []immichAsset{})
		return
	}

	assets, err := s.store.SearchRandom(r.Context(), ps.takenAfter, ps.takenBefore, ps.size, ps.mediaType)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	items := make([]immichAsset, 0, len(assets))
	for _, a := range assets {
		items = append(items, toImmichAsset(a))
	}
	writeJSON(w, http.StatusOK, items)
}

type memoryBucket struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	MemoryAt string         `json:"memoryAt"`
	Data     map[string]int `json:"data"`
	Assets   []immichAsset  `json:"assets"`
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now().UTC()
	month, day, currentYear := int(now.Month()), now.Day(), now.Year()

	if forParam := q.Get("for"); forParam != "" {
		parsed, err := parseOptionalTime(forParam)
		if err != nil || parsed == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid 'for' date"})
			return
		}
		month, day, currentYear = int(parsed.Month()), parsed.Day(), parsed.Year()
	} else if dayStr, monthStr := q.Get("day"), q.Get("month"); dayStr != "" && monthStr != "" {
		d, err1 := strconv.Atoi(dayStr)
		m, err2 := strconv.Atoi(monthStr)
		if err1 != nil || err2 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid 'day' or 'month'"})
			return
		}
		month, day = m, d
	}

	assets, err := s.store.MemoriesForDate(r.Context(), month, day, currentYear)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	byYear := make(map[int][]immichAsset)
	var years []int
	for _, a := range assets {
		if _, exists := byYear[a.TakenYear]; !exists {
			years = append(years, a.TakenYear)
		}
		byYear[a.TakenYear] = append(byYear[a.TakenYear], toImmichAsset(a))
	}

	buckets := make([]memoryBucket, 0, len(years))
	for _, y := range years {
		memAt := time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		buckets = append(buckets, memoryBucket{
			ID:       fmt.Sprintf("on-this-day-%04d-%02d-%02d", y, month, day),
			Type:     "on_this_day",
			MemoryAt: memAt.Format(time.RFC3339),
			Data:     map[string]int{"year": y},
			Assets:   byYear[y],
		})
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) lookupAsset(w http.ResponseWriter, r *http.Request) *store.Asset {
	id := r.PathValue("id")
	asset, err := s.store.GetAsset(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return nil
	}
	if asset == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Asset not found"})
		return nil
	}
	return asset
}

func (s *Server) handleGetAsset(w http.ResponseWriter, r *http.Request) {
	asset := s.lookupAsset(w, r)
	if asset == nil {
		return
	}
	writeJSON(w, http.StatusOK, toImmichAsset(*asset))
}

func (s *Server) handleAssetPreview(w http.ResponseWriter, r *http.Request) {
	s.serveAssetRendition(w, r, preview.PreviewKind, s.cache.GetOrGenerate)
}

func (s *Server) handleAssetBackdrop(w http.ResponseWriter, r *http.Request) {
	s.serveAssetRendition(w, r, preview.BackdropKind, s.cache.GetOrGenerateBackdrop)
}

func (s *Server) handleAssetStream(w http.ResponseWriter, r *http.Request) {
	asset := s.lookupAsset(w, r)
	if asset == nil {
		return
	}
	if !strings.EqualFold(asset.MediaType, "VIDEO") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "Asset is not a video"})
		return
	}

	q := r.URL.Query()
	var windowSec float64
	if rawWin := strings.TrimSpace(q.Get("window")); rawWin != "" {
		if parsedWin, err := strconv.ParseFloat(rawWin, 64); err == nil && parsedWin > 0 {
			windowSec = parsedWin
		}
	}
	var requestedFPS int
	if rawFPS := strings.TrimSpace(q.Get("fps")); rawFPS != "" {
		if parsedFPS, err := strconv.Atoi(rawFPS); err == nil && parsedFPS > 0 {
			requestedFPS = parsedFPS
		}
	}

	sourcePath := filepath.Join(s.libraryDir, filepath.FromSlash(asset.RelPath))
	clip, err := s.cache.GetOrGenerateVideoClip(r.Context(), asset.ID, asset.Mtime, sourcePath, windowSec, requestedFPS)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	flusher, _ := w.(http.Flusher)
	var flushFn func()
	if flusher != nil {
		flushFn = flusher.Flush
	}

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if flushFn != nil {
		flushFn()
	}

	_ = s.cache.StreamMJPEG(r.Context(), w, flushFn, clip, asset.DurationSec)
}

func (s *Server) serveAssetRendition(
	w http.ResponseWriter,
	r *http.Request,
	kind preview.RenditionKind,
	generate func(rCtx context.Context, assetID, sourcePath string, orientation int) (preview.Result, error),
) {
	asset := s.lookupAsset(w, r)
	if asset == nil {
		return
	}

	etag := kind.ETag(asset.ID, asset.Mtime)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	sourcePath := filepath.Join(s.libraryDir, filepath.FromSlash(asset.RelPath))
	res, err := generate(r.Context(), asset.ID, sourcePath, asset.Orientation)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	if len(res.Data) > 0 {
		http.ServeContent(w, r, kind.Filename(asset.ID), time.Unix(asset.Mtime, 0), bytes.NewReader(res.Data))
		return
	}
	http.ServeFile(w, r, res.Path)
}

func (s *Server) handleAdminScan(w http.ResponseWriter, r *http.Request) {
	stats, err := s.scanner.Scan(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func parseOptionalTime(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			return &u, nil
		}
	}
	return nil, fmt.Errorf("unsupported time format: %s", s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
