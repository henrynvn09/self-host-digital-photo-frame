package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"photo-api/internal/scanner"
)

func installFFmpegSpy(t *testing.T, transcodeDelaySec string) (countCalls func(substr string) int) {
	t.Helper()

	realFFmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("locate real ffmpeg: %v", err)
	}

	spyBinDir := t.TempDir()
	spyLogDir := t.TempDir()

	wrapperScript := "#!/bin/sh\n" +
		"log_file=$(mktemp \"" + spyLogDir + "/ffmpeg.XXXXXX\")\n" +
		"printf '%s\\n' \"$*\" > \"$log_file\"\n" +
		"if [ -n \"$FFMPEG_TRANSCODE_DELAY_SEC\" ]; then\n" +
		"  case \"$*\" in\n" +
		"    *libx264*) sleep \"$FFMPEG_TRANSCODE_DELAY_SEC\" ;;\n" +
		"  esac\n" +
		"fi\n" +
		"exec \"" + realFFmpeg + "\" \"$@\"\n"

	wrapperPath := filepath.Join(spyBinDir, "ffmpeg")
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("write ffmpeg wrapper: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", spyBinDir+string(os.PathListSeparator)+origPath)
	t.Setenv("FFMPEG_TRANSCODE_DELAY_SEC", transcodeDelaySec)

	return func(substr string) int {
		entries, err := os.ReadDir(spyLogDir)
		if err != nil {
			t.Fatalf("read spy log dir: %v", err)
		}
		matched := 0
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(spyLogDir, entry.Name()))
			if err != nil {
				continue
			}
			if substr == "" || strings.Contains(string(raw), substr) {
				matched++
			}
		}
		return matched
	}
}

func TestVideoAndImageWarmEndpointMemoryCache(t *testing.T) {
	env := setupTestEnv(t, 1024, "memory")

	photoRel := "2024/Family/warm_photo_20240715.png"
	photoPath := filepath.Join(env.photosDir, filepath.FromSlash(photoRel))
	writePNG(t, photoPath, 800, 600)

	videoRel := "2024/Family/warm_clip_20240715.mp4"
	videoPath := filepath.Join(env.photosDir, filepath.FromSlash(videoRel))
	synthesizeTestVideo(t, videoPath, 640, 360, 12, 3.5, true, 0)

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", rec.Code, rec.Body.String())
	}

	photoID := scanner.DeterministicUUID(photoRel)
	videoID := scanner.DeterministicUUID(videoRel)

	// 1. Auth and 404 contracts on POST /api/assets/{id}/warm
	if rec := env.doJSON(t, "POST", "/api/assets/"+videoID+"/warm?window=2&fps=12", nil, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key on /warm, got %d", rec.Code)
	}
	if rec := env.doJSON(t, "POST", "/api/assets/non-existent-id/warm?window=2&fps=12", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown asset on /warm, got %d", rec.Code)
	}

	// 2. Warm VIDEO asset -> warms both Backdrop JPEG and bounded (id, mtime, window=2, fps=12) MP4 clip in RAM with zero disk writes
	warmVidRec := env.doJSON(t, "POST", "/api/assets/"+videoID+"/warm?window=2&fps=12", nil, true)
	if warmVidRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on video /warm, got %d: %s", warmVidRec.Code, warmVidRec.Body.String())
	}
	var warmPayload map[string]any
	if err := json.Unmarshal(warmVidRec.Body.Bytes(), &warmPayload); err != nil {
		t.Fatalf("decode video /warm JSON: %v", err)
	}
	if warmPayload["id"] != videoID || warmPayload["type"] != "VIDEO" {
		t.Fatalf("unexpected video /warm JSON payload: %+v", warmPayload)
	}

	// Warm IMAGE asset -> warms both Backdrop JPEG and Preview JPEG in RAM
	warmImgRec := env.doJSON(t, "POST", "/api/assets/"+photoID+"/warm", nil, true)
	if warmImgRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on image /warm, got %d: %s", warmImgRec.Code, warmImgRec.Body.String())
	}

	// Verify zero disk writes in CACHE_MODE=memory
	if _, statErr := os.Stat(filepath.Join(env.dataDir, "cache")); !os.IsNotExist(statErr) {
		entries, _ := os.ReadDir(filepath.Join(env.dataDir, "cache"))
		if len(entries) > 0 {
			t.Fatalf("expected zero disk cache files in memory mode after /warm, found %d", len(entries))
		}
	}

	// Remove BOTH source files from disk to prove all renditions are served purely from RAM LRU
	if err := os.Remove(videoPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(photoPath); err != nil {
		t.Fatal(err)
	}

	// Verify VIDEO Backdrop is served from RAM LRU
	vidBackRec := env.doJSON(t, "GET", "/api/assets/"+videoID+"/backdrop", nil, true)
	if vidBackRec.Code != http.StatusOK {
		t.Fatalf("expected cached video backdrop 200 after source removal, got %d: %s", vidBackRec.Code, vidBackRec.Body.String())
	}
	vidBackImg, err := jpeg.Decode(bytes.NewReader(vidBackRec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode cached video backdrop: %v", err)
	}
	if vidBackImg.Bounds().Dx() != 360 || vidBackImg.Bounds().Dy() != 640 {
		t.Fatalf("expected 360x640 video backdrop, got %dx%d", vidBackImg.Bounds().Dx(), vidBackImg.Bounds().Dy())
	}

	// Verify IMAGE Backdrop + Preview are served from RAM LRU
	imgBackRec := env.doJSON(t, "GET", "/api/assets/"+photoID+"/backdrop", nil, true)
	if imgBackRec.Code != http.StatusOK {
		t.Fatalf("expected cached image backdrop 200 after source removal, got %d", imgBackRec.Code)
	}
	imgPrevRec := env.doJSON(t, "GET", "/api/assets/"+photoID+"/thumbnail", nil, true)
	if imgPrevRec.Code != http.StatusOK {
		t.Fatalf("expected cached image preview 200 after source removal, got %d", imgPrevRec.Code)
	}
	imgPrev, err := jpeg.Decode(bytes.NewReader(imgPrevRec.Body.Bytes()))
	if err != nil || imgPrev.Bounds().Dx() != 800 || imgPrev.Bounds().Dy() != 600 {
		t.Fatalf("expected 800x600 cached image preview, got err=%v", err)
	}

	// Verify VIDEO MJPEG stream for matching (id, mtime, window=2, fps=12) serves from RAM LRU
	ts := httptest.NewServer(env.httpHandler)
	defer ts.Close()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(streamCtx, "GET", ts.URL+"/api/assets/"+videoID+"/stream?window=2&fps=12", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-key", env.apiKey)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		streamCancel()
		t.Fatalf("expected 200 on warmed /stream after source deletion, got %d: %s", resp.StatusCode, string(body))
	}
	frames := readMJPEGFrames(t, resp, 4, streamCancel)
	_ = resp.Body.Close()
	if len(frames) < 4 {
		t.Fatalf("expected >= 4 MJPEG frames from warmed RAM clip, got %d", len(frames))
	}

	// Distinct window=1.0 was NOT warmed -> misses LRU and returns non-200 because source file was removed
	missReq, _ := http.NewRequest("GET", ts.URL+"/api/assets/"+videoID+"/stream?window=1&fps=12", nil)
	missReq.Header.Set("x-api-key", env.apiKey)
	missResp, err := ts.Client().Do(missReq)
	if err != nil {
		t.Fatalf("GET uncached window /stream: %v", err)
	}
	_ = missResp.Body.Close()
	if missResp.StatusCode == http.StatusOK {
		t.Fatalf("expected cache miss for distinct window=1 after source deletion, got 200")
	}
}

func TestVideoWarmAndStreamSingleflightDeduplication(t *testing.T) {
	countFFmpeg := installFFmpegSpy(t, "0.3")
	env := setupTestEnv(t, 1024, "memory")

	videoRel := "2024/Dedupe/shared_20240715.mp4"
	videoPath := filepath.Join(env.photosDir, filepath.FromSlash(videoRel))
	synthesizeTestVideo(t, videoPath, 480, 270, 10, 3.0, false, 0)

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", rec.Code, rec.Body.String())
	}

	// Reset spy count baseline after test fixture synthesis & scan
	baselineTranscodes := countFFmpeg("libx264")
	videoID := scanner.DeterministicUUID(videoRel)

	ts := httptest.NewServer(env.httpHandler)
	defer ts.Close()

	var wg sync.WaitGroup
	warmCodes := make([]int, 2)
	warmErrs := make([]error, 2)

	// Caller 0 (starts first and cancels after 40ms mid-transcode): must NOT kill the shared singleflight transcode!
	var canceledElapsed time.Duration
	var canceledErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/api/assets/"+videoID+"/warm?window=1.5&fps=10", nil)
		req.Header.Set("x-api-key", env.apiKey)
		resp, err := ts.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		canceledElapsed = time.Since(start)
		canceledErr = err
	}()

	// Let Caller 0 initiate the shared singleflight transcode before Callers 1, 2, and Stream join
	time.Sleep(15 * time.Millisecond)

	for i := 0; i < 2; i++ {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", ts.URL+"/api/assets/"+videoID+"/warm?window=1.5&fps=10", nil)
			req.Header.Set("x-api-key", env.apiKey)
			resp, err := ts.Client().Do(req)
			if err != nil {
				warmErrs[idx] = err
				return
			}
			defer resp.Body.Close()
			_, _ = io.ReadAll(resp.Body)
			warmCodes[idx] = resp.StatusCode
		}()
	}

	var streamStatus int
	var streamFrameCount int
	var streamErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/assets/"+videoID+"/stream?window=1.5&fps=10", nil)
		req.Header.Set("x-api-key", env.apiKey)
		resp, err := ts.Client().Do(req)
		if err != nil {
			streamErr = err
			return
		}
		defer resp.Body.Close()
		streamStatus = resp.StatusCode
		if resp.StatusCode == http.StatusOK {
			frames := readMJPEGFrames(t, resp, 3, cancel)
			streamFrameCount = len(frames)
		}
	}()

	wg.Wait()

	if canceledErr == nil {
		t.Fatalf("expected early-canceled /warm caller to abort with context error")
	}
	if canceledElapsed > 250*time.Millisecond {
		t.Fatalf("expected canceled /warm caller to return promptly (<250ms), took %v", canceledElapsed)
	}
	for i := 0; i < 2; i++ {
		if warmErrs[i] != nil || warmCodes[i] != http.StatusOK {
			t.Fatalf("concurrent /warm caller %d failed: status=%d err=%v", i, warmCodes[i], warmErrs[i])
		}
	}
	if streamErr != nil || streamStatus != http.StatusOK || streamFrameCount < 3 {
		t.Fatalf("concurrent /stream caller failed: status=%d frames=%d err=%v", streamStatus, streamFrameCount, streamErr)
	}

	actualTranscodes := countFFmpeg("libx264") - baselineTranscodes
	if actualTranscodes != 1 {
		t.Fatalf("expected singleflight to spawn exactly 1 ffmpeg H.264 transcode across 4 concurrent /warm & /stream callers, got %d", actualTranscodes)
	}
}

func TestCorruptVideoResilienceAndNegativeCache(t *testing.T) {
	countFFmpeg := installFFmpegSpy(t, "")
	env := setupTestEnv(t, 1024, "memory")

	corruptRel := "2024/Corrupt/broken_clip_20240715.mp4"
	corruptPath := filepath.Join(env.photosDir, filepath.FromSlash(corruptRel))
	synthesizeTestVideo(t, corruptPath, 320, 180, 10, 3.0, false, 0)

	if rec := env.doJSON(t, "POST", "/api/admin/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %d %s", rec.Code, rec.Body.String())
	}

	videoID := scanner.DeterministicUUID(corruptRel)

	// Corrupt the video file on disk after indexing (simulating truncated/bit-rotted media on NAS)
	corruptMtime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(corruptPath, []byte("corrupt-truncated-mp4-stream-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(corruptPath, corruptMtime, corruptMtime); err != nil {
		t.Fatal(err)
	}

	baselineAll := countFFmpeg("")

	// 1. First /warm request detects corrupt video, returns explicit 422 error status, and records negative-cache entry
	warm1 := env.doJSON(t, "POST", "/api/assets/"+videoID+"/warm?window=2&fps=10", nil, true)
	if warm1.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 Unprocessable Entity on corrupt video /warm, got %d: %s", warm1.Code, warm1.Body.String())
	}
	callsAfterFirstFailure := countFFmpeg("") - baselineAll
	if callsAfterFirstFailure != 1 {
		t.Fatalf("expected exactly 1 ffmpeg attempt on first corrupt /warm, got %d", callsAfterFirstFailure)
	}

	// 2. Repeated /warm, /stream, /backdrop, and /thumbnail requests hit negative cache fast with ZERO extra ffmpeg spawns
	for _, reqSpec := range []struct {
		method string
		path   string
	}{
		{method: "POST", path: "/api/assets/" + videoID + "/warm?window=2&fps=10"},
		{method: "POST", path: "/api/assets/" + videoID + "/warm?window=5&fps=24"},
		{method: "GET", path: "/api/assets/" + videoID + "/stream?window=2&fps=10"},
		{method: "GET", path: "/api/assets/" + videoID + "/backdrop"},
		{method: "GET", path: "/api/assets/" + videoID + "/thumbnail"},
	} {
		start := time.Now()
		rec := env.doJSON(t, reqSpec.method, reqSpec.path, nil, true)
		elapsed := time.Since(start)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 from negative cache on %s %s, got %d: %s",
				reqSpec.method, reqSpec.path, rec.Code, rec.Body.String())
		}
		if elapsed > 100*time.Millisecond {
			t.Fatalf("expected fast negative-cache rejection (<100ms) on %s %s, took %v",
				reqSpec.method, reqSpec.path, elapsed)
		}
	}

	if gotExtra := (countFFmpeg("") - baselineAll) - callsAfterFirstFailure; gotExtra != 0 {
		t.Fatalf("expected 0 additional ffmpeg spawns while negative-cached, got %d", gotExtra)
	}

	// 3. Replacing/fixing the file on disk (updating mtime) invalidates negative cache automatically!
	synthesizeTestVideo(t, corruptPath, 320, 180, 10, 3.0, false, 0)
	fixedMtime := corruptMtime.Add(10 * time.Second)
	if err := os.Chtimes(corruptPath, fixedMtime, fixedMtime); err != nil {
		t.Fatal(err)
	}

	warmFixed := env.doJSON(t, "POST", "/api/assets/"+videoID+"/warm?window=2&fps=10", nil, true)
	if warmFixed.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /warm immediately after fixing video file on disk (new mtime), got %d: %s",
			warmFixed.Code, warmFixed.Body.String())
	}

	ts := httptest.NewServer(env.httpHandler)
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/assets/"+videoID+"/stream?window=2&fps=10", nil)
	req.Header.Set("x-api-key", env.apiKey)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /stream after fix: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on /stream after fix, got %d", resp.StatusCode)
	}
	frames := readMJPEGFrames(t, resp, 3, cancel)
	if len(frames) < 3 {
		t.Fatalf("expected valid MJPEG frames after fixing corrupt video, got %d", len(frames))
	}
}
