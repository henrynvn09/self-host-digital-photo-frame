package api_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"photo-api/internal/api"
	"photo-api/internal/preview"
	"photo-api/internal/scanner"
	"photo-api/internal/store"
)

type testEnv struct {
	photosDir   string
	dataDir     string
	geoDir      string
	apiKey      string
	store       *store.Store
	cache       *preview.Cache
	scanner     *scanner.Scanner
	server      *api.Server
	httpHandler http.Handler
}

func setupTestEnv(t *testing.T, cacheMaxMB int64, cacheMode ...string) *testEnv {
	t.Helper()
	photosDir := t.TempDir()
	dataDir := t.TempDir()
	geoDir := t.TempDir()

	writeGeoNamesFixture(t, geoDir)

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mode := "memory"
	if len(cacheMode) > 0 && cacheMode[0] != "" {
		mode = cacheMode[0]
	}

	cache, err := preview.NewCache(dataDir, preview.Options{
		Mode:        mode,
		MaxWidth:    2560,
		MaxHeight:   2560,
		JPEGQuality: 88,
		MaxMB:       cacheMaxMB,
	})
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	sc := scanner.New(photosDir, geoDir, []string{"Screenshots"}, st, cache)
	srv := api.NewServer(photosDir, "secret-key", st, sc, cache)

	return &testEnv{
		photosDir:   photosDir,
		dataDir:     dataDir,
		geoDir:      geoDir,
		apiKey:      "secret-key",
		store:       st,
		cache:       cache,
		scanner:     sc,
		server:      srv,
		httpHandler: srv.Handler(),
	}
}

func (e *testEnv) doJSON(t *testing.T, method, path string, body any, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("x-api-key", e.apiKey)
	}
	rec := httptest.NewRecorder()
	e.httpHandler.ServeHTTP(rec, req)
	return rec
}

func TestEndToEndPhotoAPI(t *testing.T) {
	env := setupTestEnv(t, 1024)

	// 1. Auth checks: ping is open, version/search require API key
	if rec := env.doJSON(t, "GET", "/api/server/ping", nil, false); rec.Code != http.StatusOK {
		t.Fatalf("expected ping 200 without auth, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "GET", "/api/server/version", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected version 401 without auth, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "GET", "/api/server/version", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("expected version 200 with auth, got %d", rec.Code)
	}

	// 2. Populate synthetic Library:
	// - EXIF JPEG with GPS in Ho Chi Minh City, Vietnam + Orientation 6 (rotate 90 CW) inside named Album
	writeJPEGWithEXIF(t, filepath.Join(env.photosDir, "2023", "Saigon Trip", "saigon.jpg"),
		400, 200, "2023:07:15 10:30:00", 6, 10.8231, 106.6297)

	// - Large 3200x1800 EXIF JPEG with GPS in Los Angeles, CA inside numeric folder (Album should be empty)
	writeJPEGWithEXIF(t, filepath.Join(env.photosDir, "2022", "07", "la_4k.jpg"),
		3200, 1800, "2022:07:15 16:00:00", 1, 33.8992, -118.0689)

	// - PNG without EXIF relying on filename date (2021-07-15)
	writePNG(t, filepath.Join(env.photosDir, "2021", "IMG_20210715_090000.png"), 600, 400)

	// - Synthetic HEIC with embedded EXIF DateTimeDigitized (CreateDate) + GPS in Ho Chi Minh City
	writeSyntheticHEICWithCreateDate(t, filepath.Join(env.photosDir, "2020", "Phone Backup", "iphone.heic"),
		"2020:07:15 08:15:00", 6, 10.8231, 106.6297)

	// - Synology system folders, excluded folders, and unsupported non-media files (must all be skipped)
	writePNG(t, filepath.Join(env.photosDir, "2023", "@eaDir", "thumb.png"), 100, 100)
	writePNG(t, filepath.Join(env.photosDir, "#recycle", "deleted.png"), 100, 100)
	writePNG(t, filepath.Join(env.photosDir, "Screenshots", "screen.png"), 100, 100)
	if err := os.WriteFile(filepath.Join(env.photosDir, "2023", "notes.txt"), []byte("not-media"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Trigger Scan via HTTP
	rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", rec.Code, rec.Body.String())
	}
	var stats scanner.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode scan stats: %v", err)
	}
	if stats.Added != 4 || stats.Deleted != 0 {
		t.Fatalf("expected 4 added, got %+v", stats)
	}
	if stats.DurationMs < 0 || stats.DurationMs > 30000 {
		t.Fatalf("expected durationMs in milliseconds, got %d", stats.DurationMs)
	}

	// Re-scanning unchanged library must skip all 4
	rec = env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 4 || stats.Added != 0 || stats.Updated != 0 {
		t.Fatalf("expected 4 skipped on incremental re-scan, got %+v", stats)
	}

	// 4. Verify POST /api/search/smart (aliased to metadata, ignores ML "query": "image")
	rec = env.doJSON(t, "POST", "/api/search/smart", map[string]any{
		"query":       "image",
		"takenAfter":  "2022-01-01T00:00:00Z",
		"takenBefore": "2023-12-31T23:59:59Z",
		"type":        "IMAGE",
		"size":        100,
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("search/smart failed: %d %s", rec.Code, rec.Body.String())
	}
	var searchResp struct {
		Assets struct {
			Total int `json:"total"`
			Items []struct {
				ID               string `json:"id"`
				OriginalPath     string `json:"originalPath"`
				OriginalFileName string `json:"originalFileName"`
				AlbumName        string `json:"albumName"`
				ExifInfo         struct {
					DateTimeOriginal string  `json:"dateTimeOriginal"`
					City             *string `json:"city"`
					State            *string `json:"state"`
					Country          *string `json:"country"`
					ExifImageWidth   int     `json:"exifImageWidth"`
					ExifImageHeight  int     `json:"exifImageHeight"`
				} `json:"exifInfo"`
			} `json:"items"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searchResp); err != nil {
		t.Fatal(err)
	}
	if searchResp.Assets.Total != 2 {
		t.Fatalf("expected 2 assets in 2022-2023, got %d", searchResp.Assets.Total)
	}

	byName := make(map[string]int)
	for i, it := range searchResp.Assets.Items {
		byName[it.OriginalFileName] = i
	}
	saigon := searchResp.Assets.Items[byName["saigon.jpg"]]
	if saigon.OriginalPath != "2023/Saigon Trip/saigon.jpg" {
		t.Fatalf("expected originalPath '2023/Saigon Trip/saigon.jpg', got %q", saigon.OriginalPath)
	}
	if saigon.AlbumName != "Saigon Trip" {
		t.Fatalf("expected albumName 'Saigon Trip', got %q", saigon.AlbumName)
	}
	if saigon.ExifInfo.City == nil || *saigon.ExifInfo.City != "Ho Chi Minh City" || *saigon.ExifInfo.Country != "Vietnam" {
		t.Fatalf("expected Ho Chi Minh City, Vietnam; got %+v", saigon.ExifInfo)
	}

	la := searchResp.Assets.Items[byName["la_4k.jpg"]]
	if la.AlbumName != "" {
		t.Fatalf("expected empty albumName for numeric folder '07', got %q", la.AlbumName)
	}
	if la.ExifInfo.ExifImageWidth != 3200 || la.ExifInfo.ExifImageHeight != 1800 {
		t.Fatalf("expected 3200x1800 dimensions, got %dx%d", la.ExifInfo.ExifImageWidth, la.ExifInfo.ExifImageHeight)
	}
	if la.ExifInfo.City == nil || *la.ExifInfo.City != "Los Angeles" || *la.ExifInfo.State != "California" || *la.ExifInfo.Country != "United States" {
		t.Fatalf("expected Los Angeles, California, United States; got %+v", la.ExifInfo)
	}

	// Verify HEIC asset extracted CreateDate (2020-07-15), GPS (Ho Chi Minh City), Orientation 6, AlbumName
	heicID := scanner.DeterministicUUID("2020/Phone Backup/iphone.heic")
	heicRec := env.doJSON(t, "GET", "/api/assets/"+heicID, nil, true)
	if heicRec.Code != http.StatusOK {
		t.Fatalf("get heic asset failed: %d %s", heicRec.Code, heicRec.Body.String())
	}
	var heicAsset struct {
		AlbumName string `json:"albumName"`
		ExifInfo  struct {
			DateTimeOriginal string  `json:"dateTimeOriginal"`
			City             *string `json:"city"`
			Orientation      string  `json:"orientation"`
		} `json:"exifInfo"`
	}
	if err := json.Unmarshal(heicRec.Body.Bytes(), &heicAsset); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(heicAsset.ExifInfo.DateTimeOriginal, "2020-07-15") {
		t.Fatalf("expected HEIC CreateDate 2020-07-15, got %q", heicAsset.ExifInfo.DateTimeOriginal)
	}
	if heicAsset.ExifInfo.City == nil || *heicAsset.ExifInfo.City != "Ho Chi Minh City" {
		t.Fatalf("expected HEIC GPS resolved to Ho Chi Minh City, got %+v", heicAsset.ExifInfo.City)
	}
	if heicAsset.ExifInfo.Orientation != "6" || heicAsset.AlbumName != "Phone Backup" {
		t.Fatalf("unexpected HEIC metadata: %+v", heicAsset)
	}

	// 5. Verify GET /api/memories with BOTH ?for=... AND ?day=15&month=7 (MMM-ImmichSlideShow v3_0)
	for _, memURL := range []string{"/api/memories?for=2025-07-15T00:00:00Z", "/api/memories?day=15&month=7"} {
		rec = env.doJSON(t, "GET", memURL, nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("memories (%s) failed: %d %s", memURL, rec.Code, rec.Body.String())
		}
		var memories []struct {
			Type   string         `json:"type"`
			Data   map[string]int `json:"data"`
			Assets []any          `json:"assets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &memories); err != nil {
			t.Fatal(err)
		}
		if len(memories) != 4 {
			t.Fatalf("expected 4 yearly memory buckets (2023, 2022, 2021, 2020) for %s, got %d", memURL, len(memories))
		}
	}

	// 6. Verify POST /api/search/random
	rec = env.doJSON(t, "POST", "/api/search/random", map[string]any{"size": 2, "type": "IMAGE"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("random failed: %d %s", rec.Code, rec.Body.String())
	}
	var randomItems []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &randomItems); err != nil {
		t.Fatal(err)
	}
	if len(randomItems) != 2 {
		t.Fatalf("expected 2 random items, got %d", len(randomItems))
	}

	// 7. Verify 2K scaling on 3200x1800 LA image + ETag 304
	thumbRec := env.doJSON(t, "GET", "/api/assets/"+la.ID+"/thumbnail", nil, true)
	if thumbRec.Code != http.StatusOK {
		t.Fatalf("thumbnail failed: %d %s", thumbRec.Code, thumbRec.Body.String())
	}
	img, err := jpeg.Decode(bytes.NewReader(thumbRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode returned jpeg: %v", err)
	}
	if img.Bounds().Dx() != 2560 || img.Bounds().Dy() != 1440 {
		t.Fatalf("expected 2560x1440 scaled preview, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	etag := thumbRec.Header().Get("ETag")
	if etag == "" || !strings.Contains(thumbRec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing cache headers: ETag=%q Cache-Control=%q", etag, thumbRec.Header().Get("Cache-Control"))
	}
	req304 := httptest.NewRequest("GET", "/api/assets/"+la.ID+"/thumbnail", nil)
	req304.Header.Set("x-api-key", env.apiKey)
	req304.Header.Set("If-None-Match", etag)
	rec304 := httptest.NewRecorder()
	env.httpHandler.ServeHTTP(rec304, req304)
	if rec304.Code != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", rec304.Code)
	}

	// 8. Verify EXIF orientation 6 (400x200 rotated 90 CW -> 200x400, not upscaled)
	rotRec := env.doJSON(t, "GET", "/api/assets/"+saigon.ID+"/thumbnail", nil, true)
	if rotRec.Code != http.StatusOK {
		t.Fatalf("saigon thumbnail failed: %d", rotRec.Code)
	}
	rotImg, err := jpeg.Decode(bytes.NewReader(rotRec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if rotImg.Bounds().Dx() != 200 || rotImg.Bounds().Dy() != 400 {
		t.Fatalf("expected EXIF-rotated 200x400 image, got %dx%d", rotImg.Bounds().Dx(), rotImg.Bounds().Dy())
	}

	// 9. Verify incremental deletion removes asset from index
	if err := os.Remove(filepath.Join(env.photosDir, "2021", "IMG_20210715_090000.png")); err != nil {
		t.Fatal(err)
	}
	rec = env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Deleted != 1 {
		t.Fatalf("expected 1 deleted asset, got %+v", stats)
	}
}

func TestPreviewCacheLRUEviction(t *testing.T) {
	// 1MB cap: generate multiple noisy 2K JPEGs (~250KB+ each) and verify oldest is evicted
	env := setupTestEnv(t, 1, "disk")

	var names []string
	for i := 0; i < 6; i++ {
		name := filepath.Join("2024", "Trip", "photo_"+string(rune('a'+i))+".png")
		names = append(names, name)
		writeNoisyPNG(t, filepath.Join(env.photosDir, name), 800, 600, uint32(i+1))
	}

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d", rec.Code)
	}

	// Request thumbnails sequentially with distinct mtimes so LRU ordering is deterministic
	for _, rel := range names {
		id := scanner.DeterministicUUID(rel)
		rec := env.doJSON(t, "GET", "/api/assets/"+id+"/thumbnail", nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("thumbnail %s failed: %d", rel, rec.Code)
		}
		time.Sleep(15 * time.Millisecond)
	}

	var totalSize int64
	var count int
	_ = filepath.WalkDir(filepath.Join(env.dataDir, "cache"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jpg") {
			return nil
		}
		info, _ := d.Info()
		totalSize += info.Size()
		count++
		return nil
	})

	if totalSize > 1024*1024 {
		t.Fatalf("expected cache size <= 1MB after LRU eviction, got %d bytes (%d files)", totalSize, count)
	}
	if count == 0 || count >= len(names) {
		t.Fatalf("expected partial LRU eviction (1..%d files remaining), got %d files (%d bytes)", len(names)-1, count, totalSize)
	}
}

func TestPrebakeBlurToggle(t *testing.T) {
	photosDir := t.TempDir()
	dataDir := t.TempDir()

	writePNG(t, filepath.Join(photosDir, "2024", "Trip", "wide.png"), 800, 400)

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cache, err := preview.NewCache(dataDir, preview.Options{
		MaxWidth:    2560,
		MaxHeight:   2560,
		JPEGQuality: 88,
		MaxMB:       100,
		PrebakeBlur: true,
		FrameWidth:  1440,
		FrameHeight: 2560,
	})
	if err != nil {
		t.Fatal(err)
	}

	sc := scanner.New(photosDir, "", nil, st, cache)
	srv := api.NewServer(photosDir, "secret-key", st, sc, cache)
	h := srv.Handler()

	scanReq := httptest.NewRequest("POST", "/api/admin/scan", nil)
	scanReq.Header.Set("x-api-key", "secret-key")
	scanRec := httptest.NewRecorder()
	h.ServeHTTP(scanRec, scanReq)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d", scanRec.Code)
	}

	id := scanner.DeterministicUUID("2024/Trip/wide.png")
	thumbReq := httptest.NewRequest("GET", "/api/assets/"+id+"/thumbnail", nil)
	thumbReq.Header.Set("x-api-key", "secret-key")
	thumbRec := httptest.NewRecorder()
	h.ServeHTTP(thumbRec, thumbReq)
	if thumbRec.Code != http.StatusOK {
		t.Fatalf("thumbnail failed: %d", thumbRec.Code)
	}

	img, err := jpeg.Decode(bytes.NewReader(thumbRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode prebaked jpeg: %v", err)
	}
	if img.Bounds().Dx() != 1440 || img.Bounds().Dy() != 2560 {
		t.Fatalf("expected 1440x2560 prebaked frame, got %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
	r, g, b, _ := img.At(720, 100).RGBA()
	if r == 0 && g == 0 && b == 0 {
		t.Fatalf("expected non-black blurred top bar pixel at (720,100)")
	}
}

func writeNoisyPNG(t *testing.T, path string, w, h int, seed uint32) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	state := seed * 2654435761
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			state = state*1664525 + 1013904223
			img.Set(x, y, color.RGBA{
				R: uint8(state >> 24),
				G: uint8(state >> 16),
				B: uint8(state >> 8),
				A: 255,
			})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeGeoNamesFixture(t *testing.T, dir string) {
	t.Helper()
	countries := "US\tUSA\t840\tUS\tUnited States\nVN\tVNM\t704\tVM\tVietnam\n"
	admin1 := "US.CA\tCalifornia\nVN.SG\tHo Chi Minh\n"
	cities := strings.Join([]string{
		"5368361\tLos Angeles\tLos Angeles\t\t34.05223\t-118.24368\tP\tPPLA2\tUS\t\tCA\t037\t\t\t3971883\t\t89\tAmerica/Los_Angeles\t2023-01-01",
		"1566083\tHo Chi Minh City\tHo Chi Minh City\t\t10.82302\t106.62965\tP\tPPLA\tVN\t\tSG\t\t\t\t9000000\t\t19\tAsia/Ho_Chi_Minh\t2023-01-01",
	}, "\n") + "\n"

	if err := os.WriteFile(filepath.Join(dir, "countryInfo.txt"), []byte(countries), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "admin1CodesASCII.txt"), []byte(admin1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cities1000.txt"), []byte(cities), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(path, ts, ts)
}

// writeJPEGWithEXIF writes a valid JPEG with an embedded EXIF APP1 segment
// containing Orientation, DateTimeOriginal, and GPS Lat/Lon.
func writeJPEGWithEXIF(t *testing.T, path string, w, h int, dt string, orientation uint16, lat, lon float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 200, A: 255})
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, img, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	raw := jpgBuf.Bytes()
	app1 := buildEXIFAPP1(dt, orientation, lat, lon)

	var out bytes.Buffer
	out.Write(raw[:2]) // SOI (FF D8)
	out.Write(app1)
	out.Write(raw[2:])
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildEXIFAPP1(dt string, orientation uint16, lat, lon float64) []byte {
	return buildEXIFAPP1WithTag(0x9003, dt, orientation, lat, lon)
}

func writeSyntheticHEICWithCreateDate(t *testing.T, path, dt string, orientation uint16, lat, lon float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Tag 0x9004 = DateTimeDigitized (CreateDate), embedded inside a minimal ISO BMFF-like header
	app1 := buildEXIFAPP1WithTag(0x9004, dt, orientation, lat, lon)
	var heic bytes.Buffer
	heic.Write([]byte{0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0, 'm', 'i', 'f', '1', 'h', 'e', 'i', 'c'})
	heic.Write(app1[4:]) // Raw "Exif\x00\x00" + TIFF payload as stored in HEIC Exif item
	if err := os.WriteFile(path, heic.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildEXIFAPP1WithTag(dateTag uint16, dt string, orientation uint16, lat, lon float64) []byte {
	var tiff bytes.Buffer
	be := binary.BigEndian

	// TIFF Header (8 bytes)
	tiff.WriteString("MM")
	_ = binary.Write(&tiff, be, uint16(42))
	_ = binary.Write(&tiff, be, uint32(8))

	// IFD0: 3 entries (Orientation, ExifIFDPointer, GPSInfoIFDPointer)
	exifIFDOffset := uint32(50)
	dtOffset := uint32(68)
	dtBytes := append([]byte(dt), 0) // 20 bytes -> ends at 88
	gpsIFDOffset := uint32(88)
	latDataOffset := uint32(142)
	lonDataOffset := uint32(166)

	// Write IFD0
	_ = binary.Write(&tiff, be, uint16(3))
	writeIFDEntry(&tiff, 0x0112, 3, 1, uint32(orientation)<<16)
	writeIFDEntry(&tiff, 0x8769, 4, 1, exifIFDOffset)
	writeIFDEntry(&tiff, 0x8825, 4, 1, gpsIFDOffset)
	_ = binary.Write(&tiff, be, uint32(0))

	// Write ExifIFD
	_ = binary.Write(&tiff, be, uint16(1))
	writeIFDEntry(&tiff, dateTag, 2, uint32(len(dtBytes)), dtOffset)
	_ = binary.Write(&tiff, be, uint32(0))
	tiff.Write(dtBytes)

	// Write GPS IFD
	latRef := byte('N')
	if lat < 0 {
		latRef = 'S'
	}
	lonRef := byte('E')
	if lon < 0 {
		lonRef = 'W'
	}
	_ = binary.Write(&tiff, be, uint16(4))
	writeIFDEntry(&tiff, 0x0001, 2, 2, uint32(latRef)<<24)
	writeIFDEntry(&tiff, 0x0002, 5, 3, latDataOffset)
	writeIFDEntry(&tiff, 0x0003, 2, 2, uint32(lonRef)<<24)
	writeIFDEntry(&tiff, 0x0004, 5, 3, lonDataOffset)
	_ = binary.Write(&tiff, be, uint32(0))

	writeDMS(&tiff, math.Abs(lat))
	writeDMS(&tiff, math.Abs(lon))

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var app1 bytes.Buffer
	app1.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&app1, be, uint16(len(payload)+2))
	app1.Write(payload)
	return app1.Bytes()
}

func writeIFDEntry(buf *bytes.Buffer, tag, typ uint16, count, val uint32) {
	be := binary.BigEndian
	_ = binary.Write(buf, be, tag)
	_ = binary.Write(buf, be, typ)
	_ = binary.Write(buf, be, count)
	_ = binary.Write(buf, be, val)
}

func writeDMS(buf *bytes.Buffer, deg float64) {
	be := binary.BigEndian
	d := uint32(math.Floor(deg))
	minFloat := (deg - float64(d)) * 60
	m := uint32(math.Floor(minFloat))
	s := uint32(math.Round((minFloat - float64(m)) * 60 * 10000))
	_ = binary.Write(buf, be, d)
	_ = binary.Write(buf, be, uint32(1))
	_ = binary.Write(buf, be, m)
	_ = binary.Write(buf, be, uint32(1))
	_ = binary.Write(buf, be, s)
	_ = binary.Write(buf, be, uint32(10000))
}

func TestAPISearchRandomDeterministic(t *testing.T) {
	env := setupTestEnv(t, 50, "memory")

	// Create 5 images in year 2024
	for i := 0; i < 5; i++ {
		name := filepath.Join("2024", "Trip", fmt.Sprintf("cycle_photo_%d.png", i))
		writePNG(t, filepath.Join(env.photosDir, name), 400, 300)
	}

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d", rec.Code)
	}

	searchBody := map[string]any{
		"size":        2,
		"type":        "IMAGE",
		"takenAfter":  "2024-01-01T00:00:00Z",
		"takenBefore": "2024-12-31T23:59:59Z",
	}

	// Call 1: size=2 -> returns 2 unique photos for today's deterministic slice
	rec1 := env.doJSON(t, "POST", "/api/search/random", searchBody, true)
	if rec1.Code != http.StatusOK {
		t.Fatalf("call 1 failed: %d", rec1.Code)
	}
	var items1 []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec1.Body.Bytes(), &items1); err != nil || len(items1) != 2 {
		t.Fatalf("expected 2 items in call 1, got %d (err: %v)", len(items1), err)
	}

	// Call 2: same day call -> must be idempotent and return identical 2 photos in same order
	rec2 := env.doJSON(t, "POST", "/api/search/random", searchBody, true)
	if rec2.Code != http.StatusOK {
		t.Fatalf("call 2 failed: %d", rec2.Code)
	}
	var items2 []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &items2); err != nil || len(items2) != 2 {
		t.Fatalf("expected 2 items in call 2, got %d (err: %v)", len(items2), err)
	}

	for i := range items1 {
		if items1[i].ID != items2[i].ID {
			t.Fatalf("expected identical idempotent output on same day at index %d: %s vs %s", i, items1[i].ID, items2[i].ID)
		}
	}
}

func TestAssetBackdropEndpoint(t *testing.T) {
	env := setupTestEnv(t, 50, "memory")

	relPath := filepath.Join("2024", "Trip", "split_contrast.png")
	fullPath := filepath.Join(env.photosDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	srcImg := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1600; x++ {
			if x < 800 {
				srcImg.Set(x, y, color.RGBA{R: 240, G: 200, B: 160, A: 255})
			} else {
				srcImg.Set(x, y, color.RGBA{R: 20, G: 40, B: 220, A: 255})
			}
		}
	}
	f, err := os.Create(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, srcImg); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", rec.Code, rec.Body.String())
	}

	id := scanner.DeterministicUUID(filepath.ToSlash(relPath))

	// 1. Auth & 404 behavior
	if rec := env.doJSON(t, "GET", "/api/assets/"+id+"/backdrop", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "GET", "/api/assets/missing-id/backdrop", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown asset, got %d", rec.Code)
	}

	// 2. Backdrop JPEG dimensions, compact size, ETag/304, ~35% darkening, and spatial blur
	backRec := env.doJSON(t, "GET", "/api/assets/"+id+"/backdrop", nil, true)
	if backRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for backdrop, got %d %s", backRec.Code, backRec.Body.String())
	}
	if ct := backRec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("expected Content-Type image/jpeg, got %q", ct)
	}
	rawBackdrop := backRec.Body.Bytes()
	if len(rawBackdrop) == 0 || len(rawBackdrop) > 60*1024 {
		t.Fatalf("expected compact backdrop JPEG (1..61440 bytes), got %d bytes", len(rawBackdrop))
	}

	etag := backRec.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("expected non-empty ETag on backdrop response")
	}
	req304 := httptest.NewRequest("GET", "/api/assets/"+id+"/backdrop", nil)
	req304.Header.Set("x-api-key", env.apiKey)
	req304.Header.Set("If-None-Match", etag)
	rec304 := httptest.NewRecorder()
	env.httpHandler.ServeHTTP(rec304, req304)
	if rec304.Code != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified for matching backdrop ETag, got %d", rec304.Code)
	}

	backImg, err := jpeg.Decode(bytes.NewReader(rawBackdrop))
	if err != nil {
		t.Fatalf("decode backdrop jpeg: %v", err)
	}
	if backImg.Bounds().Dx() != 360 || backImg.Bounds().Dy() != 640 {
		t.Fatalf("expected 360x640 downscaled portrait backdrop, got %dx%d", backImg.Bounds().Dx(), backImg.Bounds().Dy())
	}

	pixelRGB8 := func(x, y int) (int, int, int) {
		r, g, b, _ := backImg.At(x, y).RGBA()
		return int(r >> 8), int(g >> 8), int(b >> 8)
	}

	// Far-left region: source R=240 darkened by ~35% (*0.65 => ~156)
	leftR, _, leftB := pixelRGB8(40, 320)
	if leftR < 135 || leftR > 175 || leftR <= leftB {
		t.Fatalf("expected ~35%% darkened warm pixel on left (R ~156 > B), got R=%d B=%d", leftR, leftB)
	}
	// Far-right region: source B=220 darkened by ~35% (*0.65 => ~143)
	rightR, _, rightB := pixelRGB8(320, 320)
	if rightB < 120 || rightB > 165 || rightB <= rightR {
		t.Fatalf("expected ~35%% darkened cool pixel on right (B ~143 > R), got R=%d B=%d", rightR, rightB)
	}
	// Center seam (x=180): blur must smoothly blend left & right rather than leaving a sharp step
	seamLeftR, _, _ := pixelRGB8(175, 320)
	seamMidR, _, _ := pixelRGB8(180, 320)
	seamRightR, _, _ := pixelRGB8(185, 320)
	if seamMidR < 55 || seamMidR > 115 {
		t.Fatalf("expected blurred mid-seam R blend (~85), got %d", seamMidR)
	}
	if math.Abs(float64(seamLeftR-seamRightR)) > 70 {
		t.Fatalf("expected blurred transition across seam (x=175 R=%d, x=185 R=%d)", seamLeftR, seamRightR)
	}

	// 3. Cache key isolation: full Preview (/thumbnail route) must NOT collide with /backdrop
	previewRec := env.doJSON(t, "GET", "/api/assets/"+id+"/thumbnail", nil, true)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for preview, got %d", previewRec.Code)
	}
	previewImg, err := jpeg.Decode(bytes.NewReader(previewRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode preview jpeg: %v", err)
	}
	if previewImg.Bounds().Dx() != 1600 || previewImg.Bounds().Dy() != 900 {
		t.Fatalf("expected full 1600x900 preview (distinct from 360x640 backdrop), got %dx%d",
			previewImg.Bounds().Dx(), previewImg.Bounds().Dy())
	}

	// 4. Zero disk writes in CACHE_MODE=memory & served from RAM LRU even if source file is removed
	if _, statErr := os.Stat(filepath.Join(env.dataDir, "cache")); !os.IsNotExist(statErr) {
		entries, _ := os.ReadDir(filepath.Join(env.dataDir, "cache"))
		if len(entries) > 0 {
			t.Fatalf("expected zero disk cache files in memory mode, got %d entries", len(entries))
		}
	}
	if err := os.Remove(fullPath); err != nil {
		t.Fatal(err)
	}
	cachedBackRec := env.doJSON(t, "GET", "/api/assets/"+id+"/backdrop", nil, true)
	if cachedBackRec.Code != http.StatusOK || !bytes.Equal(cachedBackRec.Body.Bytes(), rawBackdrop) {
		t.Fatalf("expected cached backdrop from RAM LRU after source removal, got status=%d len=%d",
			cachedBackRec.Code, cachedBackRec.Body.Len())
	}
}

func synthesizeTestVideo(t *testing.T, path string, width, height, fps int, durationSec float64, withAudio bool, rotateDeg int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ext := strings.ToLower(filepath.Ext(path))

	targetPath := path
	var rawTmp string
	if rotateDeg != 0 {
		rawTmp = path + ".raw.mp4"
		targetPath = rawTmp
		defer func() { _ = os.Remove(rawTmp) }()
	}

	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=%dx%d:rate=%d", width, height, fps),
	}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440")
	}
	args = append(args, "-t", strconv.FormatFloat(durationSec, 'f', -1, 64))
	if ext == ".webm" && rotateDeg == 0 {
		args = append(args, "-c:v", "libvpx", "-b:v", "200k", "-pix_fmt", "yuv420p")
		if withAudio {
			args = append(args, "-c:a", "libvorbis")
		}
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
		if withAudio {
			args = append(args, "-c:a", "aac")
		}
		if ext == ".m4v" && rotateDeg == 0 {
			args = append(args, "-f", "ipod")
		}
	}
	args = append(args, targetPath)

	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("synthesize video %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}

	if rotateDeg != 0 {
		rotArgs := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-i", rawTmp,
			"-c", "copy",
			"-metadata:s:v:0", fmt.Sprintf("rotate=%d", rotateDeg),
			path,
		}
		if out, err := exec.Command("ffmpeg", rotArgs...).CombinedOutput(); err != nil {
			t.Fatalf("apply rotation %d to %s: %v (%s)", rotateDeg, path, err, strings.TrimSpace(string(out)))
		}
	}
}

func readMJPEGFrames(t *testing.T, resp *http.Response, stopAfter int, cancel context.CancelFunc) []image.Image {
	t.Helper()
	ct := resp.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parse Content-Type %q: %v", ct, err)
	}
	if mediaType != "multipart/x-mixed-replace" {
		t.Fatalf("expected multipart/x-mixed-replace, got %q", mediaType)
	}
	boundary := params["boundary"]
	if boundary != "frame" {
		t.Fatalf("expected boundary=frame, got %q", boundary)
	}

	mr := multipart.NewReader(resp.Body, boundary)
	var frames []image.Image
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if stopAfter > 0 && len(frames) >= stopAfter {
				break
			}
			t.Fatalf("read multipart frame %d: %v", len(frames), err)
		}
		raw, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			if stopAfter > 0 && len(frames) >= stopAfter {
				break
			}
			t.Fatalf("read frame body %d: %v", len(frames), err)
		}
		img, err := jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode JPEG frame %d (%d bytes): %v", len(frames), len(raw), err)
		}
		frames = append(frames, img)
		if stopAfter > 0 && len(frames) >= stopAfter {
			if cancel != nil {
				cancel()
			}
			break
		}
	}
	return frames
}

func TestVideoIndexingSearchBackdropAndMJPEGStream(t *testing.T) {
	env := setupTestEnv(t, 1024, "memory")

	// 1. Populate mixed photo + video Library across .mp4, .MOV, .m4v, and .webm
	photoRel := "2023/Birthday Party/photo_20230715.png"
	writePNG(t, filepath.Join(env.photosDir, filepath.FromSlash(photoRel)), 600, 400)

	// Long >1080p 60fps video with audio (5.0s, 2000x1124, 60fps) -> tests trimming, 1080p downscale, 30/target fps cap, and -an
	longRel := "2023/Birthday Party/cake_20230715.mp4"
	synthesizeTestVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(longRel)), 2000, 1124, 60, 5.0, true, 0)

	// Rotated portrait QuickTime .MOV (3.0s, 320x180 coded with rotate=90 -> 180x320 display, 24fps)
	rotRel := "2021/Portrait Walk/vertical_20210715.MOV"
	synthesizeTestVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(rotRel)), 320, 180, 24, 3.0, false, 90)

	// Short clip for server-side looping (.m4v, 1.0s, 320x180, 10fps)
	shortRel := "2022/Summer/loop_20220715.m4v"
	longShortFullPath := filepath.Join(env.photosDir, filepath.FromSlash(shortRel))
	synthesizeTestVideo(t, longShortFullPath, 320, 180, 10, 1.0, false, 0)

	// WebM clip (.webm, 1.5s, 320x240, 15fps)
	webmRel := "2020/Web Clips/clip_20200715.webm"
	synthesizeTestVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(webmRel)), 320, 240, 15, 1.5, false, 0)

	// Corrupt video asset -> Scan records an error instead of freezing a 0s/0x0 row in SQLite
	corruptRel := "2020/Web Clips/broken_20200715.mp4"
	corruptFullPath := filepath.Join(env.photosDir, filepath.FromSlash(corruptRel))
	if err := os.WriteFile(corruptFullPath, []byte("not-a-valid-mp4"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Initial Scan indexes all 5 valid assets and records 1 error for the unreadable video; removing the broken video leaves 5 skipped
	scanRec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", scanRec.Code, scanRec.Body.String())
	}
	var stats scanner.Stats
	if err := json.Unmarshal(scanRec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Added != 5 || stats.Errors != 1 {
		t.Fatalf("expected 5 added (1 photo + 4 videos) and 1 probe error, got %+v", stats)
	}
	if err := os.Remove(corruptFullPath); err != nil {
		t.Fatal(err)
	}

	rescanRec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if err := json.Unmarshal(rescanRec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 5 || stats.Added != 0 || stats.Updated != 0 {
		t.Fatalf("expected all 5 unchanged assets skipped on rescan, got %+v", stats)
	}

	// 3. Verify GET /api/assets/{id} metadata for rotated portrait .MOV
	rotID := scanner.DeterministicUUID(rotRel)
	assetRec := env.doJSON(t, "GET", "/api/assets/"+rotID, nil, true)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("get rotated video asset failed: %d %s", assetRec.Code, assetRec.Body.String())
	}
	var rotMeta struct {
		ID              string  `json:"id"`
		Type            string  `json:"type"`
		DurationSeconds float64 `json:"durationSeconds"`
		AlbumName       string  `json:"albumName"`
		ExifInfo        struct {
			Orientation     string `json:"orientation"`
			ExifImageWidth  int    `json:"exifImageWidth"`
			ExifImageHeight int    `json:"exifImageHeight"`
		} `json:"exifInfo"`
	}
	if err := json.Unmarshal(assetRec.Body.Bytes(), &rotMeta); err != nil {
		t.Fatal(err)
	}
	if rotMeta.Type != "VIDEO" {
		t.Fatalf("expected type VIDEO, got %q", rotMeta.Type)
	}
	if math.Abs(rotMeta.DurationSeconds-3.0) > 0.25 {
		t.Fatalf("expected durationSeconds ~3.0, got %v", rotMeta.DurationSeconds)
	}
	if rotMeta.ExifInfo.ExifImageWidth != 180 || rotMeta.ExifInfo.ExifImageHeight != 320 {
		t.Fatalf("expected 90-deg swapped dimensions 180x320, got %dx%d",
			rotMeta.ExifInfo.ExifImageWidth, rotMeta.ExifInfo.ExifImageHeight)
	}
	if rotMeta.ExifInfo.Orientation != "1" {
		t.Fatalf("expected normalized orientation '1', got %q", rotMeta.ExifInfo.Orientation)
	}
	if rotMeta.AlbumName != "Portrait Walk" {
		t.Fatalf("expected albumName 'Portrait Walk', got %q", rotMeta.AlbumName)
	}

	// 4. Unified & filtered search across metadata, smart, random, and memories
	type assetItem struct {
		ID              string  `json:"id"`
		Type            string  `json:"type"`
		DurationSeconds float64 `json:"durationSeconds"`
	}
	for _, tc := range []struct {
		typeFilter string
		wantTotal  int
		wantImages int
		wantVideos int
	}{
		{typeFilter: "", wantTotal: 5, wantImages: 1, wantVideos: 4},
		{typeFilter: "ALL", wantTotal: 5, wantImages: 1, wantVideos: 4},
		{typeFilter: "IMAGE", wantTotal: 1, wantImages: 1, wantVideos: 0},
		{typeFilter: "VIDEO", wantTotal: 4, wantImages: 0, wantVideos: 4},
		{typeFilter: "AUDIO", wantTotal: 0, wantImages: 0, wantVideos: 0},
	} {
		for _, ep := range []string{"/api/search/metadata", "/api/search/smart"} {
			rec := env.doJSON(t, "POST", ep, map[string]any{"type": tc.typeFilter, "size": 50}, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s (type=%q) failed: %d", ep, tc.typeFilter, rec.Code)
			}
			var body struct {
				Assets struct {
					Total int         `json:"total"`
					Items []assetItem `json:"items"`
				} `json:"assets"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Assets.Total != tc.wantTotal || len(body.Assets.Items) != tc.wantTotal {
				t.Fatalf("%s (type=%q): expected %d items, got total=%d len=%d",
					ep, tc.typeFilter, tc.wantTotal, body.Assets.Total, len(body.Assets.Items))
			}
		}

		randRec := env.doJSON(t, "POST", "/api/search/random", map[string]any{"type": tc.typeFilter, "size": 50}, true)
		if randRec.Code != http.StatusOK {
			t.Fatalf("random (type=%q) failed: %d", tc.typeFilter, randRec.Code)
		}
		var randItems []assetItem
		if err := json.Unmarshal(randRec.Body.Bytes(), &randItems); err != nil {
			t.Fatal(err)
		}
		gotImg, gotVid := 0, 0
		for _, it := range randItems {
			switch it.Type {
			case "IMAGE":
				gotImg++
				if it.DurationSeconds != 0 {
					t.Fatalf("expected IMAGE durationSeconds=0, got %v", it.DurationSeconds)
				}
			case "VIDEO":
				gotVid++
				if it.DurationSeconds <= 0 {
					t.Fatalf("expected VIDEO durationSeconds>0, got %v", it.DurationSeconds)
				}
			default:
				t.Fatalf("unexpected item type %q", it.Type)
			}
		}
		if gotImg != tc.wantImages || gotVid != tc.wantVideos {
			t.Fatalf("random (type=%q): expected %d images & %d videos, got %d images & %d videos",
				tc.typeFilter, tc.wantImages, tc.wantVideos, gotImg, gotVid)
		}
	}

	// Bounded anniversary /api/search/random returns unified photo + video pool by default
	annivRec := env.doJSON(t, "POST", "/api/search/random", map[string]any{
		"takenAfter":  "2023-07-01T00:00:00Z",
		"takenBefore": "2023-07-31T23:59:59Z",
		"size":        10,
	}, true)
	var annivItems []assetItem
	if err := json.Unmarshal(annivRec.Body.Bytes(), &annivItems); err != nil {
		t.Fatal(err)
	}
	if len(annivItems) != 2 {
		t.Fatalf("expected 2 unified 2023 anniversary assets (1 photo + 1 video), got %d", len(annivItems))
	}

	// GET /api/memories returns both IMAGE and VIDEO assets on July 15 across years
	memRec := env.doJSON(t, "GET", "/api/memories?for=2025-07-15T00:00:00Z", nil, true)
	if memRec.Code != http.StatusOK {
		t.Fatalf("memories failed: %d", memRec.Code)
	}
	var memoryGroups []struct {
		Assets []assetItem `json:"assets"`
	}
	if err := json.Unmarshal(memRec.Body.Bytes(), &memoryGroups); err != nil {
		t.Fatal(err)
	}
	if len(memoryGroups) != 4 {
		t.Fatalf("expected 4 yearly memory entries (2023, 2022, 2021, 2020), got %d", len(memoryGroups))
	}

	// 5. Video /thumbnail poster & /backdrop pre-blurred JPEG in memory mode with zero disk writes
	vidThumbRec := env.doJSON(t, "GET", "/api/assets/"+rotID+"/thumbnail", nil, true)
	if vidThumbRec.Code != http.StatusOK {
		t.Fatalf("video thumbnail failed: %d %s", vidThumbRec.Code, vidThumbRec.Body.String())
	}
	vidThumbImg, err := jpeg.Decode(bytes.NewReader(vidThumbRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode video poster jpeg: %v", err)
	}
	if vidThumbImg.Bounds().Dx() != 180 || vidThumbImg.Bounds().Dy() != 320 {
		t.Fatalf("expected auto-rotated 180x320 video poster, got %dx%d",
			vidThumbImg.Bounds().Dx(), vidThumbImg.Bounds().Dy())
	}

	vidBackRec := env.doJSON(t, "GET", "/api/assets/"+rotID+"/backdrop", nil, true)
	if vidBackRec.Code != http.StatusOK {
		t.Fatalf("video backdrop failed: %d %s", vidBackRec.Code, vidBackRec.Body.String())
	}
	vidBackImg, err := jpeg.Decode(bytes.NewReader(vidBackRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode video backdrop jpeg: %v", err)
	}
	if vidBackImg.Bounds().Dx() != 360 || vidBackImg.Bounds().Dy() != 640 {
		t.Fatalf("expected 360x640 blurred video backdrop, got %dx%d",
			vidBackImg.Bounds().Dx(), vidBackImg.Bounds().Dy())
	}

	// 6. MJPEG /api/assets/{id}/stream: auth, 404, 400 on IMAGE, trimming, 1080p cap, rotation, looping, LRU keying, and disconnect abort
	photoID := scanner.DeterministicUUID(photoRel)
	longID := scanner.DeterministicUUID(longRel)
	shortID := scanner.DeterministicUUID(shortRel)

	if rec := env.doJSON(t, "GET", "/api/assets/"+longID+"/stream?window=2&fps=10", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key on /stream, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "GET", "/api/assets/missing-id/stream?window=2&fps=10", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown asset on /stream, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "GET", "/api/assets/"+photoID+"/stream?window=2&fps=10", nil, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when streaming an IMAGE asset, got %d", rec.Code)
	}

	ts := httptest.NewServer(env.httpHandler)
	defer ts.Close()

	doStream := func(ctx context.Context, path string) *http.Response {
		req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("x-api-key", env.apiKey)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return resp
	}

	// 6a. Long 5s 2000x1124@60fps video trimmed to window=2&fps=10 -> ~20 frames in ~2s, downscaled <=1920x1080 even dims
	trimStart := time.Now()
	trimResp := doStream(context.Background(), "/api/assets/"+longID+"/stream?window=2&fps=10")
	if trimResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(trimResp.Body)
		_ = trimResp.Body.Close()
		t.Fatalf("expected 200 on long /stream, got %d: %s", trimResp.StatusCode, string(body))
	}
	if cc := trimResp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("expected Cache-Control: no-store on /stream, got %q", cc)
	}
	trimFrames := readMJPEGFrames(t, trimResp, 0, nil)
	_ = trimResp.Body.Close()
	trimElapsed := time.Since(trimStart)

	if len(trimFrames) < 17 || len(trimFrames) > 23 {
		t.Fatalf("expected ~20 MJPEG frames for window=2&fps=10 on 5s clip, got %d", len(trimFrames))
	}
	if trimElapsed < 1500*time.Millisecond || trimElapsed > 4500*time.Millisecond {
		t.Fatalf("expected ~2s paced stream duration, got %v", trimElapsed)
	}
	fw, fh := trimFrames[0].Bounds().Dx(), trimFrames[0].Bounds().Dy()
	if fw != 1920 || fh > 1080 || fw%2 != 0 || fh%2 != 0 {
		t.Fatalf("expected 1080p-capped even dimensions (1920x<=1080) from 2000x1124 source, got %dx%d", fw, fh)
	}

	// 6b. Rotated portrait 320x180 (rotate=90, 24fps) streamed with window=1&fps=30 -> 180x320 frames capped at source 24fps
	rotResp := doStream(context.Background(), "/api/assets/"+rotID+"/stream?window=1&fps=30")
	if rotResp.StatusCode != http.StatusOK {
		_ = rotResp.Body.Close()
		t.Fatalf("expected 200 on rotated /stream, got %d", rotResp.StatusCode)
	}
	rotFrames := readMJPEGFrames(t, rotResp, 0, nil)
	_ = rotResp.Body.Close()
	if len(rotFrames) < 20 || len(rotFrames) > 26 {
		t.Fatalf("expected ~24 frames (preserving 24fps <= 30fps cap) in 1s window, got %d", len(rotFrames))
	}
	if rotFrames[0].Bounds().Dx() != 180 || rotFrames[0].Bounds().Dy() != 320 {
		t.Fatalf("expected auto-rotated portrait 180x320 MJPEG frames, got %dx%d",
			rotFrames[0].Bounds().Dx(), rotFrames[0].Bounds().Dy())
	}

	// 6c. Short 1.0s@10fps clip with window=2.5&fps=10 -> loops seamlessly (~25 frames across ~2.5s)
	loopStart := time.Now()
	loopResp := doStream(context.Background(), "/api/assets/"+shortID+"/stream?window=2.5&fps=10")
	if loopResp.StatusCode != http.StatusOK {
		_ = loopResp.Body.Close()
		t.Fatalf("expected 200 on looped /stream, got %d", loopResp.StatusCode)
	}
	loopFrames := readMJPEGFrames(t, loopResp, 0, nil)
	_ = loopResp.Body.Close()
	loopElapsed := time.Since(loopStart)
	if len(loopFrames) < 22 || len(loopFrames) > 28 {
		t.Fatalf("expected ~25 looped MJPEG frames for 1s clip over 2.5s@10fps window, got %d", len(loopFrames))
	}
	if loopElapsed < 2000*time.Millisecond || loopElapsed > 4500*time.Millisecond {
		t.Fatalf("expected ~2.5s looped stream duration, got %v", loopElapsed)
	}

	// 6d. Verify zero disk writes in memory mode + RAM LRU cache keyed by (id, mtime, window, fps)
	if _, statErr := os.Stat(filepath.Join(env.dataDir, "cache")); !os.IsNotExist(statErr) {
		entries, _ := os.ReadDir(filepath.Join(env.dataDir, "cache"))
		if len(entries) > 0 {
			t.Fatalf("expected zero disk cache files in memory mode, found %d", len(entries))
		}
	}
	if err := os.Remove(longShortFullPath); err != nil {
		t.Fatal(err)
	}
	// Same (id, mtime, window=2.5, fps=10) serves from RAM LRU even with source file removed!
	cachedCtx, cachedCancel := context.WithCancel(context.Background())
	cachedResp := doStream(cachedCtx, "/api/assets/"+shortID+"/stream?window=2.5&fps=10")
	if cachedResp.StatusCode != http.StatusOK {
		cachedCancel()
		_ = cachedResp.Body.Close()
		t.Fatalf("expected RAM LRU hit (200) for same (id,mtime,window,fps) after source deletion, got %d", cachedResp.StatusCode)
	}
	cachedFrames := readMJPEGFrames(t, cachedResp, 3, cachedCancel)
	_ = cachedResp.Body.Close()
	if len(cachedFrames) < 3 {
		t.Fatalf("expected frames streamed from RAM LRU, got %d", len(cachedFrames))
	}
	// Different window (window=1.5) misses cache and fails cleanly since source file was deleted
	missResp := doStream(context.Background(), "/api/assets/"+shortID+"/stream?window=1.5&fps=10")
	_ = missResp.Body.Close()
	if missResp.StatusCode == http.StatusOK {
		t.Fatalf("expected cache miss for distinct window=1.5 after source removal, got 200")
	}

	// 6e. Immediate termination on client disconnect (10s window canceled after 2 frames finishes in << 1s)
	discCtx, discCancel := context.WithCancel(context.Background())
	discStart := time.Now()
	discResp := doStream(discCtx, "/api/assets/"+longID+"/stream?window=10&fps=10")
	if discResp.StatusCode != http.StatusOK {
		discCancel()
		_ = discResp.Body.Close()
		t.Fatalf("expected 200 on disconnect test stream, got %d", discResp.StatusCode)
	}
	_ = readMJPEGFrames(t, discResp, 2, discCancel)
	_ = discResp.Body.Close()
	discElapsed := time.Since(discStart)
	if discElapsed > 2500*time.Millisecond {
		t.Fatalf("expected client disconnect to abort 10s stream promptly (<2.5s), took %v", discElapsed)
	}
}
