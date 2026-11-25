package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"photo-api/internal/api"
	"photo-api/internal/config"
	"photo-api/internal/preview"
	"photo-api/internal/scanner"
)

type videoMetaSpec struct {
	durationSec float64
	formatTags  map[string]string
	streamTags  map[string]string
	mtime       time.Time
}

func synthesizeTaggedVideo(t *testing.T, path string, spec videoMetaSpec) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ext := strings.ToLower(filepath.Ext(path))

	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10",
		"-t", strconv.FormatFloat(spec.durationSec, 'f', -1, 64),
	}
	if ext == ".webm" {
		args = append(args, "-c:v", "libvpx", "-b:v", "200k", "-pix_fmt", "yuv420p")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p")
		if ext == ".mov" || ext == ".mp4" {
			args = append(args, "-movflags", "+use_metadata_tags")
		}
	}
	for k, v := range spec.formatTags {
		args = append(args, "-metadata", fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range spec.streamTags {
		args = append(args, "-metadata:s:v:0", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, path)

	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("synthesize tagged video %s: %v (%s)", path, err, strings.TrimSpace(string(out)))
	}
	if !spec.mtime.IsZero() {
		if err := os.Chtimes(path, spec.mtime, spec.mtime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
}

func appendTokyoGeoNamesFixture(t *testing.T, geoDir string) {
	t.Helper()
	appendLine := func(name, line string) {
		f, err := os.OpenFile(filepath.Join(geoDir, name), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	appendLine("countryInfo.txt", "JP\tJPN\t392\tJA\tJapan\n")
	appendLine("admin1CodesASCII.txt", "JP.40\tTokyo\n")
	appendLine("cities1000.txt", "1850147\tTokyo\tTokyo\t\t35.6895\t139.69171\tP\tPPLC\tJP\t\t40\t\t\t\t8336599\t\t40\tAsia/Tokyo\t2023-01-01\n")
}

func setupConfigDrivenEnv(t *testing.T, cacheMode string) *testEnv {
	t.Helper()
	cfg := config.Load()
	return newTestEnv(t, preview.Options{
		Mode:           cacheMode,
		MaxWidth:       cfg.MaxWidth,
		MaxHeight:      cfg.MaxHeight,
		JPEGQuality:    cfg.JPEGQuality,
		MaxMB:          128,
		VideoMaxWidth:  cfg.VideoMaxWidth,
		VideoMaxHeight: cfg.VideoMaxHeight,
		VideoFPS:       cfg.VideoFPS,
	}, scanner.Options{
		MinVideoSeconds:     cfg.MinVideoSeconds,
		LivePhotoMaxSeconds: cfg.LivePhotoMaxSeconds,
	}, appendTokyoGeoNamesFixture)
}

func TestVideoQuickTimeDatesAndISO6709GeoNames(t *testing.T) {
	env := setupConfigDrivenEnv(t, "memory")

	// 1. iPhone QuickTime .MOV recorded at 2021-07-15T20:30:00-0700 in Los Angeles.
	//    Notice mvhd creation_time in UTC is 2021-07-16T03:30:00Z (next UTC day!), and file mtime is 2025.
	//    Scan must prefer com.apple.quicktime.creationdate and preserve camera wall-clock date/time
	//    (July 15, 2021 20:30:00) so July 15 anniversary queries match 2021!
	laMovRel := "Imports/Birthday Dinner/iphone_clip.mov"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(laMovRel)), videoMetaSpec{
		durationSec: 4.0,
		formatTags: map[string]string{
			"com.apple.quicktime.creationdate":     "2021-07-15T20:30:00-0700",
			"creation_time":                        "2021-07-16T03:30:00Z",
			"com.apple.quicktime.location.ISO6709": "+33.8992-118.0689/",
		},
		mtime: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 2. QuickTime .MOV with colon offset (+09:00, early morning July 15 in Tokyo where UTC is July 14!)
	//    plus ISO-6709 with altitude and Apple CRSWGS_84 suffix (+35.6895+139.6917+40.000CRSWGS_84/).
	tokyoMovRel := "Imports/Tokyo Morning/shinjuku.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(tokyoMovRel)), videoMetaSpec{
		durationSec: 4.0,
		formatTags: map[string]string{
			"com.apple.quicktime.creationdate":     "2020-07-15T01:15:00+09:00",
			"com.apple.quicktime.location.ISO6709": "+35.6895+139.6917+40.000CRSWGS_84/",
		},
		mtime: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 3. Standard MP4 with creation_time (Z) and ISO-6709 `location` tag in Ho Chi Minh City.
	saigonMp4Rel := "Imports/Saigon Street/scooter.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(saigonMp4Rel)), videoMetaSpec{
		durationSec: 4.0,
		formatTags: map[string]string{
			"creation_time": "2019-07-15T14:20:05Z",
			"location":      "+10.8231+106.6297/",
		},
		mtime: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 4. Stream-tag fallback (.webm where QuickTime date & ISO-6709 location reside on streams[0].tags rather than format.tags).
	streamTagRel := "Imports/Web Stream/stream_tagged.webm"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(streamTagRel)), videoMetaSpec{
		durationSec: 4.0,
		streamTags: map[string]string{
			"com.apple.quicktime.creationdate": "2015-07-15T11:22:33-0700",
			"LOCATION":                         "+33.8992-118.0689/",
		},
		mtime: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 5. Fallback 1: Dummy zeroed epoch creation_time (1970-01-01T00:00:00Z) -> falls back to filename date (2018-07-15).
	fnFallbackRel := "Imports/Misc/VID_20180715_183000.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(fnFallbackRel)), videoMetaSpec{
		durationSec: 4.0,
		formatTags: map[string]string{
			"creation_time": "1970-01-01T00:00:00Z",
		},
		mtime: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 6. Fallback 2: No metadata or filename date -> falls back to directory YYYY/MM (2017/07 -> 2017-07-01).
	dirFallbackRel := "2017/07/family_gathering.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(dirFallbackRel)), videoMetaSpec{
		durationSec: 4.0,
		mtime:       time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	// 7. Fallback 3: No metadata, filename date, or directory date -> falls back to file mtime (2016-07-15T09:10:11Z).
	mtimeFallbackRel := "Archive/Clips/untitled_clip.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(mtimeFallbackRel)), videoMetaSpec{
		durationSec: 4.0,
		mtime:       time.Date(2016, 7, 15, 9, 10, 11, 0, time.UTC),
	})

	scanRec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", scanRec.Code, scanRec.Body.String())
	}

	type assetDTO struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		AlbumName string `json:"albumName"`
		ExifInfo  struct {
			DateTimeOriginal string  `json:"dateTimeOriginal"`
			City             *string `json:"city"`
			State            *string `json:"state"`
			Country          *string `json:"country"`
		} `json:"exifInfo"`
	}

	fetchAsset := func(rel string) assetDTO {
		t.Helper()
		id := scanner.DeterministicUUID(rel)
		rec := env.doJSON(t, "GET", "/api/assets/"+id, nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/assets/%s (%s) failed: %d %s", id, rel, rec.Code, rec.Body.String())
		}
		var out assetDTO
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode asset %s: %v", rel, err)
		}
		return out
	}

	strVal := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	la := fetchAsset(laMovRel)
	if la.ExifInfo.DateTimeOriginal != "2021-07-15T20:30:00Z" {
		t.Fatalf("LA .mov DateTimeOriginal: got %q, want %q", la.ExifInfo.DateTimeOriginal, "2021-07-15T20:30:00Z")
	}
	if strVal(la.ExifInfo.City) != "Los Angeles" || strVal(la.ExifInfo.State) != "California" || strVal(la.ExifInfo.Country) != "United States" {
		t.Fatalf("LA .mov GeoNames: got city=%s state=%s country=%s",
			strVal(la.ExifInfo.City), strVal(la.ExifInfo.State), strVal(la.ExifInfo.Country))
	}

	tokyo := fetchAsset(tokyoMovRel)
	if tokyo.ExifInfo.DateTimeOriginal != "2020-07-15T01:15:00Z" {
		t.Fatalf("Tokyo .MOV DateTimeOriginal: got %q, want %q", tokyo.ExifInfo.DateTimeOriginal, "2020-07-15T01:15:00Z")
	}
	if strVal(tokyo.ExifInfo.City) != "Tokyo" || strVal(tokyo.ExifInfo.State) != "Tokyo" || strVal(tokyo.ExifInfo.Country) != "Japan" {
		t.Fatalf("Tokyo .MOV GeoNames: got city=%s state=%s country=%s",
			strVal(tokyo.ExifInfo.City), strVal(tokyo.ExifInfo.State), strVal(tokyo.ExifInfo.Country))
	}

	saigon := fetchAsset(saigonMp4Rel)
	if saigon.ExifInfo.DateTimeOriginal != "2019-07-15T14:20:05Z" {
		t.Fatalf("Saigon .mp4 DateTimeOriginal: got %q, want %q", saigon.ExifInfo.DateTimeOriginal, "2019-07-15T14:20:05Z")
	}
	if strVal(saigon.ExifInfo.City) != "Ho Chi Minh City" || strVal(saigon.ExifInfo.State) != "Ho Chi Minh" || strVal(saigon.ExifInfo.Country) != "Vietnam" {
		t.Fatalf("Saigon .mp4 GeoNames: got city=%s state=%s country=%s",
			strVal(saigon.ExifInfo.City), strVal(saigon.ExifInfo.State), strVal(saigon.ExifInfo.Country))
	}

	streamTagged := fetchAsset(streamTagRel)
	if streamTagged.ExifInfo.DateTimeOriginal != "2015-07-15T11:22:33Z" {
		t.Fatalf("stream-tagged .webm DateTimeOriginal: got %q, want %q", streamTagged.ExifInfo.DateTimeOriginal, "2015-07-15T11:22:33Z")
	}
	if strVal(streamTagged.ExifInfo.City) != "Los Angeles" || strVal(streamTagged.ExifInfo.Country) != "United States" {
		t.Fatalf("stream-tagged .webm GeoNames: got city=%s country=%s",
			strVal(streamTagged.ExifInfo.City), strVal(streamTagged.ExifInfo.Country))
	}

	fnFallback := fetchAsset(fnFallbackRel)
	if fnFallback.ExifInfo.DateTimeOriginal != "2018-07-15T12:00:00Z" {
		t.Fatalf("filename fallback DateTimeOriginal: got %q, want %q", fnFallback.ExifInfo.DateTimeOriginal, "2018-07-15T12:00:00Z")
	}

	dirFallback := fetchAsset(dirFallbackRel)
	if dirFallback.ExifInfo.DateTimeOriginal != "2017-07-01T12:00:00Z" {
		t.Fatalf("directory fallback DateTimeOriginal: got %q, want %q", dirFallback.ExifInfo.DateTimeOriginal, "2017-07-01T12:00:00Z")
	}

	mtimeFallback := fetchAsset(mtimeFallbackRel)
	if mtimeFallback.ExifInfo.DateTimeOriginal != "2016-07-15T09:10:11Z" {
		t.Fatalf("mtime fallback DateTimeOriginal: got %q, want %q", mtimeFallback.ExifInfo.DateTimeOriginal, "2016-07-15T09:10:11Z")
	}

	// Verify anniversary / memories query on July 15 returns all 6 July-15 videos (2021, 2020, 2019, 2018, 2016, 2015)!
	memRec := env.doJSON(t, "GET", "/api/memories?for=2026-07-15T00:00:00Z", nil, true)
	if memRec.Code != http.StatusOK {
		t.Fatalf("GET /api/memories failed: %d %s", memRec.Code, memRec.Body.String())
	}
	var buckets []struct {
		Data   map[string]int `json:"data"`
		Assets []assetDTO     `json:"assets"`
	}
	if err := json.Unmarshal(memRec.Body.Bytes(), &buckets); err != nil {
		t.Fatal(err)
	}
	var gotYears []int
	for _, b := range buckets {
		gotYears = append(gotYears, b.Data["year"])
	}
	wantYears := []int{2021, 2020, 2019, 2018, 2016, 2015}
	if len(gotYears) != len(wantYears) {
		t.Fatalf("memories years on July 15: got %v, want %v", gotYears, wantYears)
	}
	for i := range wantYears {
		if gotYears[i] != wantYears[i] {
			t.Fatalf("memories years[%d]: got %v, want %v", i, gotYears, wantYears)
		}
	}
}

func TestVideoMicroClipAndLivePhotoFiltering(t *testing.T) {
	env := setupConfigDrivenEnv(t, "memory")

	// 1. Standalone micro-clip < MIN_VIDEO_SECONDS (2.2s < 3.0s) -> EXCLUDED
	microRel := "2023/Trip/micro_2_2s.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(microRel)), videoMetaSpec{
		durationSec: 2.2,
	})

	// 2. Paired Live Photo companion <= 3.5s (IMG_1234.JPG + IMG_1234.MOV of 3.2s) -> photo KEPT, .MOV EXCLUDED
	liveImg1Rel := "2023/Trip/IMG_1234.JPG"
	writeJPEGWithEXIF(t, filepath.Join(env.photosDir, filepath.FromSlash(liveImg1Rel)),
		320, 240, "2023:07:15 10:00:00", 1, 33.8992, -118.0689)
	liveMov1Rel := "2023/Trip/IMG_1234.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(liveMov1Rel)), videoMetaSpec{
		durationSec: 3.2,
	})

	// 3. Paired Live Photo where uppercase .MOV lexically precedes lowercase .png (IMG_2000.MOV 3.4s + img_2000.png)
	//    -> .png KEPT, .MOV EXCLUDED (order-independent & case-insensitive stem match!)
	liveMov2Rel := "2023/Trip/IMG_2000.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(liveMov2Rel)), videoMetaSpec{
		durationSec: 3.4,
	})
	liveImg2Rel := "2023/Trip/img_2000.png"
	writePNG(t, filepath.Join(env.photosDir, filepath.FromSlash(liveImg2Rel)), 320, 240)

	// 3b. Exact 3.5s paired Live Photo companion (IMG_3500.JPG + IMG_3500.MOV of 3.5s) -> photo KEPT, .MOV EXCLUDED (<= 3.5s boundary)
	liveImg35Rel := "2023/Trip/IMG_3500.JPG"
	writeJPEGWithEXIF(t, filepath.Join(env.photosDir, filepath.FromSlash(liveImg35Rel)),
		320, 240, "2023:07:15 10:30:00", 1, 33.8992, -118.0689)
	liveMov35Rel := "2023/Trip/IMG_3500.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(liveMov35Rel)), videoMetaSpec{
		durationSec: 3.5,
	})

	// 4. Standalone 3.2s video in the SAME directory as photos with DIFFERENT stems -> KEPT
	standalone32Rel := "2023/Trip/standalone_3_2s.mov"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(standalone32Rel)), videoMetaSpec{
		durationSec: 3.2,
	})

	// 5. Exact 3.0s standalone video -> KEPT (>= MIN_VIDEO_SECONDS)
	exact30Rel := "2023/Trip/exact_3_0s.mp4"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(exact30Rel)), videoMetaSpec{
		durationSec: 3.0,
	})

	// 6. 3.2s video with stem IMG_1234 in a DIFFERENT directory (no sibling image there) -> KEPT
	otherDirMovRel := "2023/OtherFolder/IMG_1234.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(otherDirMovRel)), videoMetaSpec{
		durationSec: 3.2,
	})

	// 7. Same-stem video > 3.5s (IMG_4000.JPG + IMG_4000.MOV of 4.2s) -> BOTH KEPT
	longStemImgRel := "2023/Trip/IMG_4000.JPG"
	writeJPEGWithEXIF(t, filepath.Join(env.photosDir, filepath.FromSlash(longStemImgRel)),
		320, 240, "2023:07:15 11:00:00", 1, 33.8992, -118.0689)
	longStemMovRel := "2023/Trip/IMG_4000.MOV"
	synthesizeTaggedVideo(t, filepath.Join(env.photosDir, filepath.FromSlash(longStemMovRel)), videoMetaSpec{
		durationSec: 4.2,
	})

	scanRec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
	if scanRec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", scanRec.Code, scanRec.Body.String())
	}
	var stats scanner.Stats
	if err := json.Unmarshal(scanRec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}

	searchRec := env.doJSON(t, "POST", "/api/search/metadata", map[string]any{"size": 100}, true)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search failed: %d %s", searchRec.Code, searchRec.Body.String())
	}
	var body struct {
		Assets struct {
			Total int `json:"total"`
			Items []struct {
				OriginalPath string `json:"originalPath"`
			} `json:"items"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(searchRec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}

	var gotPaths []string
	for _, it := range body.Assets.Items {
		gotPaths = append(gotPaths, it.OriginalPath)
	}
	sort.Strings(gotPaths)

	wantPaths := []string{
		exact30Rel,
		liveImg1Rel,
		liveImg35Rel,
		longStemImgRel,
		longStemMovRel,
		liveImg2Rel,
		standalone32Rel,
		otherDirMovRel,
	}
	sort.Strings(wantPaths)

	if strings.Join(gotPaths, "|") != strings.Join(wantPaths, "|") {
		t.Fatalf("indexed paths mismatch:\n got: %v\nwant: %v", gotPaths, wantPaths)
	}

	for _, excludedRel := range []string{microRel, liveMov1Rel, liveMov2Rel, liveMov35Rel} {
		id := scanner.DeterministicUUID(excludedRel)
		if rec := env.doJSON(t, "GET", "/api/assets/"+id, nil, true); rec.Code != http.StatusNotFound {
			t.Fatalf("expected excluded video %s to return 404, got %d", excludedRel, rec.Code)
		}
	}

	// Verify setting MIN_VIDEO_SECONDS=0 and LIVE_PHOTO_MAX_SECONDS=0 disables both filters.
	t.Setenv("MIN_VIDEO_SECONDS", "0")
	t.Setenv("LIVE_PHOTO_MAX_SECONDS", "0")
	disabledCfg := config.Load()
	if disabledCfg.MinVideoSeconds != 0 || disabledCfg.LivePhotoMaxSeconds != 0 {
		t.Fatalf("expected 0 thresholds from config.Load(), got min=%v live=%v", disabledCfg.MinVideoSeconds, disabledCfg.LivePhotoMaxSeconds)
	}
	disabledScanner := scanner.New(env.photosDir, env.geoDir, []string{"Screenshots"}, env.store, env.cache, scanner.Options{
		MinVideoSeconds:     disabledCfg.MinVideoSeconds,
		LivePhotoMaxSeconds: disabledCfg.LivePhotoMaxSeconds,
	})
	disabledSrv := api.NewServer(env.photosDir, env.apiKey, env.store, disabledScanner, env.cache)
	scanDisabledReq := httptest.NewRequest("POST", "/api/admin/scan", nil)
	scanDisabledReq.Header.Set("x-api-key", env.apiKey)
	scanDisabledRec := httptest.NewRecorder()
	disabledSrv.Handler().ServeHTTP(scanDisabledRec, scanDisabledReq)
	if scanDisabledRec.Code != http.StatusOK {
		t.Fatalf("disabled-filter scan failed: %d", scanDisabledRec.Code)
	}
	for _, rel := range []string{microRel, liveMov1Rel, liveMov2Rel, liveMov35Rel} {
		id := scanner.DeterministicUUID(rel)
		if rec := env.doJSON(t, "GET", "/api/assets/"+id, nil, true); rec.Code != http.StatusOK {
			t.Fatalf("expected %s indexed when filters disabled (=0), got %d", rel, rec.Code)
		}
	}
}

func TestNewlyExcludedVideosPrunedAndEvictedFromCache(t *testing.T) {
	for _, cacheMode := range []string{"memory", "disk"} {
		t.Run(cacheMode, func(t *testing.T) {
			env := setupConfigDrivenEnv(t, cacheMode)
			ctx := context.Background()

			// Phase A: IMG_7777.MOV (3.2s) arrives BEFORE companion photo IMG_7777.JPG.
			movRel := "2023/LiveArrival/IMG_7777.MOV"
			movFull := filepath.Join(env.photosDir, filepath.FromSlash(movRel))
			synthesizeTaggedVideo(t, movFull, videoMetaSpec{
				durationSec: 3.2,
				formatTags: map[string]string{
					"com.apple.quicktime.creationdate": "2023-07-15T15:00:00-0700",
				},
			})

			statBefore, err := os.Stat(movFull)
			if err != nil {
				t.Fatal(err)
			}

			// Scan 1: standalone 3.2s .MOV is indexed
			scan1Rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
			if scan1Rec.Code != http.StatusOK {
				t.Fatalf("scan 1 failed: %d", scan1Rec.Code)
			}
			var stats1 scanner.Stats
			_ = json.Unmarshal(scan1Rec.Body.Bytes(), &stats1)
			if stats1.Added != 1 {
				t.Fatalf("expected 1 added on scan 1, got %+v", stats1)
			}

			movID := scanner.DeterministicUUID(movRel)
			movMtime := statBefore.ModTime().Unix()

			// Warm all 3 renditions (preview poster, backdrop, and MJPEG video preview) via HTTP!
			if rec := env.doJSON(t, "GET", "/api/assets/"+movID+"/thumbnail", nil, true); rec.Code != http.StatusOK {
				t.Fatalf("warm preview failed: %d", rec.Code)
			}
			if rec := env.doJSON(t, "GET", "/api/assets/"+movID+"/backdrop", nil, true); rec.Code != http.StatusOK {
				t.Fatalf("warm backdrop failed: %d", rec.Code)
			}
			ts := httptest.NewServer(env.httpHandler)
			defer ts.Close()
			streamReq, _ := http.NewRequest("GET", ts.URL+"/api/assets/"+movID+"/stream?window=1&fps=10", nil)
			streamReq.Header.Set("x-api-key", env.apiKey)
			streamResp, err := ts.Client().Do(streamReq)
			if err != nil {
				t.Fatalf("stream request failed: %v", err)
			}
			if streamResp.StatusCode != http.StatusOK {
				_ = streamResp.Body.Close()
				t.Fatalf("expected 200 on stream, got %d", streamResp.StatusCode)
			}
			frames := readMJPEGFrames(t, streamResp, 2, nil)
			_ = streamResp.Body.Close()
			if len(frames) < 2 {
				t.Fatalf("expected >=2 MJPEG frames, got %d", len(frames))
			}

			// Prove all 3 renditions ARE cached right now (succeeds even with non-existent sourcePath!)
			bogusSource := filepath.Join(env.photosDir, "does-not-exist.mov")
			if _, err := env.cache.GetOrGenerate(ctx, movID, bogusSource, 1); err != nil {
				t.Fatalf("expected preview hit from cache before prune, got err: %v", err)
			}
			if _, err := env.cache.GetOrGenerateBackdrop(ctx, movID, bogusSource, 1); err != nil {
				t.Fatalf("expected backdrop hit from cache before prune, got err: %v", err)
			}
			if _, err := env.cache.GetOrGenerateVideoPreview(ctx, movID, movMtime, bogusSource, 1, 10); err != nil {
				t.Fatalf("expected video preview hit from cache before prune, got err: %v", err)
			}

			// Now companion photo IMG_7777.JPG arrives in the same directory; IMG_7777.MOV (mtime, size) is untouched!
			jpgRel := "2023/LiveArrival/IMG_7777.JPG"
			writeJPEGWithEXIF(t, filepath.Join(env.photosDir, filepath.FromSlash(jpgRel)),
				320, 240, "2023:07:15 15:00:00", 1, 33.8992, -118.0689)

			statAfter, err := os.Stat(movFull)
			if err != nil {
				t.Fatal(err)
			}
			if statAfter.ModTime().Unix() != statBefore.ModTime().Unix() || statAfter.Size() != statBefore.Size() {
				t.Fatalf("expected IMG_7777.MOV mtime/size untouched")
			}

			// Scan 2: adds IMG_7777.JPG AND prunes + evicts IMG_7777.MOV!
			scan2Rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true)
			if scan2Rec.Code != http.StatusOK {
				t.Fatalf("scan 2 failed: %d", scan2Rec.Code)
			}
			var stats2 scanner.Stats
			_ = json.Unmarshal(scan2Rec.Body.Bytes(), &stats2)
			if stats2.Added != 1 || stats2.Deleted != 1 {
				t.Fatalf("expected Added=1 (JPG) and Deleted=1 (pruned Live Photo MOV), got %+v", stats2)
			}

			if rec := env.doJSON(t, "GET", "/api/assets/"+movID, nil, true); rec.Code != http.StatusNotFound {
				t.Fatalf("expected pruned MOV to return 404, got %d", rec.Code)
			}

			// Verify all 3 renditions WERE evicted from preview.Cache (bogus sourcePath now misses cache and errors!)
			if _, err := env.cache.GetOrGenerate(ctx, movID, bogusSource, 1); err == nil {
				t.Fatalf("expected preview cache entry evicted after prune, but got cache hit!")
			}
			if _, err := env.cache.GetOrGenerateBackdrop(ctx, movID, bogusSource, 1); err == nil {
				t.Fatalf("expected backdrop cache entry evicted after prune, but got cache hit!")
			}
			if _, err := env.cache.GetOrGenerateVideoPreview(ctx, movID, movMtime, bogusSource, 1, 10); err == nil {
				t.Fatalf("expected video preview cache entry evicted after prune, but got cache hit!")
			}
			if cacheMode == "disk" {
				matches, _ := filepath.Glob(filepath.Join(env.dataDir, "cache", "*", movID+"*"))
				if len(matches) != 0 {
					t.Fatalf("expected 0 disk cache files remaining for evicted MOV, found %v", matches)
				}
			}

			// Phase B: Raising MIN_VIDEO_SECONDS via env prunes a previously indexed 4.0s standalone video & evicts cache!
			clip4Rel := "2023/Threshold/standalone_4s.mp4"
			clip4Full := filepath.Join(env.photosDir, filepath.FromSlash(clip4Rel))
			synthesizeTaggedVideo(t, clip4Full, videoMetaSpec{durationSec: 4.0})
			clip4Stat, err := os.Stat(clip4Full)
			if err != nil {
				t.Fatal(err)
			}
			clip4ID := scanner.DeterministicUUID(clip4Rel)
			clip4Mtime := clip4Stat.ModTime().Unix()

			if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
				t.Fatalf("scan 3 failed: %d", rec.Code)
			}
			if rec := env.doJSON(t, "GET", "/api/assets/"+clip4ID+"/thumbnail", nil, true); rec.Code != http.StatusOK {
				t.Fatalf("warm clip4 preview failed: %d", rec.Code)
			}
			if rec := env.doJSON(t, "GET", "/api/assets/"+clip4ID+"/backdrop", nil, true); rec.Code != http.StatusOK {
				t.Fatalf("warm clip4 backdrop failed: %d", rec.Code)
			}
			if _, err := env.cache.GetOrGenerateVideoPreview(ctx, clip4ID, clip4Mtime, clip4Full, 1, 10); err != nil {
				t.Fatalf("warm clip4 video preview failed: %v", err)
			}

			t.Setenv("MIN_VIDEO_SECONDS", "5.0")
			higherCfg := config.Load()
			if higherCfg.MinVideoSeconds != 5.0 {
				t.Fatalf("expected MIN_VIDEO_SECONDS=5.0 from config.Load(), got %v", higherCfg.MinVideoSeconds)
			}
			higherScanner := scanner.New(env.photosDir, env.geoDir, []string{"Screenshots"}, env.store, env.cache, scanner.Options{
				MinVideoSeconds:     higherCfg.MinVideoSeconds,
				LivePhotoMaxSeconds: higherCfg.LivePhotoMaxSeconds,
			})
			higherSrv := api.NewServer(env.photosDir, env.apiKey, env.store, higherScanner, env.cache)
			higherReq := httptest.NewRequest("POST", "/api/admin/scan", nil)
			higherReq.Header.Set("x-api-key", env.apiKey)
			higherRec := httptest.NewRecorder()
			higherSrv.Handler().ServeHTTP(higherRec, higherReq)
			if higherRec.Code != http.StatusOK {
				t.Fatalf("higher threshold scan failed: %d", higherRec.Code)
			}
			var stats4 scanner.Stats
			_ = json.Unmarshal(higherRec.Body.Bytes(), &stats4)
			if stats4.Deleted != 1 {
				t.Fatalf("expected Deleted=1 when MIN_VIDEO_SECONDS raised to 5.0, got %+v", stats4)
			}
			if rec := env.doJSON(t, "GET", "/api/assets/"+clip4ID, nil, true); rec.Code != http.StatusNotFound {
				t.Fatalf("expected 4.0s video pruned under MIN_VIDEO_SECONDS=5.0, got %d", rec.Code)
			}
			if _, err := env.cache.GetOrGenerate(ctx, clip4ID, bogusSource, 1); err == nil {
				t.Fatalf("expected clip4 preview evicted")
			}
			if _, err := env.cache.GetOrGenerateBackdrop(ctx, clip4ID, bogusSource, 1); err == nil {
				t.Fatalf("expected clip4 backdrop evicted")
			}
			if _, err := env.cache.GetOrGenerateVideoPreview(ctx, clip4ID, clip4Mtime, bogusSource, 1, 10); err == nil {
				t.Fatalf("expected clip4 video preview evicted")
			}
		})
	}
}
