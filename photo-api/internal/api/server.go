package api

import (
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
	photosDir string
	apiKey    string
	store     *store.Store
	scanner   *scanner.Scanner
	cache     *preview.Cache
	mux       *http.ServeMux
}

func NewServer(photosDir, apiKey string, st *store.Store, sc *scanner.Scanner, cache *preview.Cache) *Server {
	s := &Server{
		photosDir: photosDir,
		apiKey:    apiKey,
		store:     st,
		scanner:   sc,
		cache:     cache,
		mux:       http.NewServeMux(),
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
	s.mux.HandleFunc("GET /api/assets/{id}/thumbnail", s.handleAssetThumbnail)
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
}

type immichAsset struct {
	ID               string         `json:"id"`
	Type             string         `json:"type"`
	OriginalFileName string         `json:"originalFileName"`
	FileCreatedAt    string         `json:"fileCreatedAt"`
	FileModifiedAt   string         `json:"fileModifiedAt"`
	LocalDateTime    string         `json:"localDateTime"`
	ExifInfo         immichExifInfo `json:"exifInfo"`
	Album            string         `json:"album,omitempty"`
}

func toImmichAsset(a store.Asset) immichAsset {
	taken := a.TakenAt.UTC().Format(time.RFC3339)
	mod := time.Unix(a.Mtime, 0).UTC().Format(time.RFC3339)
	return immichAsset{
		ID:               a.ID,
		Type:             "IMAGE",
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
		},
		Album: a.Album,
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

func (s *Server) handleSearchMetadata(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Type != "" && !strings.EqualFold(req.Type, "IMAGE") {
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

	takenAfter, err := parseOptionalTime(req.TakenAfter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid takenAfter"})
		return
	}
	takenBefore, err := parseOptionalTime(req.TakenBefore)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid takenBefore"})
		return
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}
	size := req.Size
	if size <= 0 {
		size = 250
	}

	res, err := s.store.SearchMetadata(r.Context(), store.SearchParams{
		TakenAfter:  takenAfter,
		TakenBefore: takenBefore,
		Order:       req.Order,
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
	var req searchRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Type != "" && !strings.EqualFold(req.Type, "IMAGE") {
		writeJSON(w, http.StatusOK, []immichAsset{})
		return
	}

	takenAfter, err := parseOptionalTime(req.TakenAfter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid takenAfter"})
		return
	}
	takenBefore, err := parseOptionalTime(req.TakenBefore)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid takenBefore"})
		return
	}

	assets, err := s.store.SearchRandom(r.Context(), takenAfter, takenBefore, req.Size)
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
	refDate := time.Now().UTC()
	if forParam := r.URL.Query().Get("for"); forParam != "" {
		parsed, err := parseOptionalTime(forParam)
		if err != nil || parsed == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid 'for' date"})
			return
		}
		refDate = parsed.UTC()
	}

	assets, err := s.store.MemoriesForDate(r.Context(), int(refDate.Month()), refDate.Day(), refDate.Year())
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
		memAt := time.Date(y, refDate.Month(), refDate.Day(), 0, 0, 0, 0, time.UTC)
		buckets = append(buckets, memoryBucket{
			ID:       fmt.Sprintf("on-this-day-%04d-%02d-%02d", y, int(refDate.Month()), refDate.Day()),
			Type:     "on_this_day",
			MemoryAt: memAt.Format(time.RFC3339),
			Data:     map[string]int{"year": y},
			Assets:   byYear[y],
		})
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) handleGetAsset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := s.store.GetAsset(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if asset == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Asset not found"})
		return
	}
	writeJSON(w, http.StatusOK, toImmichAsset(*asset))
}

func (s *Server) handleAssetThumbnail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	asset, err := s.store.GetAsset(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if asset == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Asset not found"})
		return
	}

	etag := fmt.Sprintf(`"%s-%d"`, asset.ID, asset.Mtime)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	sourcePath := filepath.Join(s.photosDir, filepath.FromSlash(asset.RelPath))
	cachedPath, err := s.cache.GetOrGenerate(r.Context(), asset.ID, sourcePath, asset.Orientation)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	http.ServeFile(w, r, cachedPath)
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
