package api_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// - Synology system folders, excluded folders, and non-image files (must all be skipped)
	writePNG(t, filepath.Join(env.photosDir, "2023", "@eaDir", "thumb.png"), 100, 100)
	writePNG(t, filepath.Join(env.photosDir, "#recycle", "deleted.png"), 100, 100)
	writePNG(t, filepath.Join(env.photosDir, "Screenshots", "screen.png"), 100, 100)
	if err := os.WriteFile(filepath.Join(env.photosDir, "2023", "clip.mp4"), []byte("not-an-image"), 0o644); err != nil {
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

func TestAPISearchRandomExhaustionCycles(t *testing.T) {
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

	// Call 1: size=2 -> returns 2 unique photos
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

	// Call 2: size=2 -> returns 2 disjoint unique photos
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

	for _, it1 := range items1 {
		for _, it2 := range items2 {
			if it1.ID == it2.ID {
				t.Fatalf("asset %s was returned in both call 1 and call 2 (sampling was not without replacement)", it1.ID)
			}
		}
	}

	// Call 3: size=2 -> only 1 unseen photo remains (5 total - 2 - 2). Under-fill: returns 1 item!
	rec3 := env.doJSON(t, "POST", "/api/search/random", searchBody, true)
	if rec3.Code != http.StatusOK {
		t.Fatalf("call 3 failed: %d", rec3.Code)
	}
	var items3 []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec3.Body.Bytes(), &items3); err != nil || len(items3) != 1 {
		t.Fatalf("expected under-fill with 1 item in call 3, got %d", len(items3))
	}

	// Call 4: Unseen pool reached 0. Auto-reset should trigger and return 2 items!
	rec4 := env.doJSON(t, "POST", "/api/search/random", searchBody, true)
	if rec4.Code != http.StatusOK {
		t.Fatalf("call 4 failed: %d", rec4.Code)
	}
	var items4 []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec4.Body.Bytes(), &items4); err != nil || len(items4) != 2 {
		t.Fatalf("expected 2 items in call 4 after auto-reset, got %d", len(items4))
	}
}
