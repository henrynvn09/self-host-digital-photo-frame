package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultVideoWindowSec = 12.0
	minVideoWindowSec     = 0.5
	maxVideoWindowSec     = 300.0
	videoTranscodeTimeout = 60 * time.Second
	maxLoopFrameBuffer    = 32 * 1024 * 1024
)

// VideoPreview represents a silent, duration-bounded, 30 fps-capped H.264 Preview rendition.
type VideoPreview struct {
	Data      []byte
	Path      string
	WindowSec float64
	FPS       float64
}

type videoPreviewKey struct {
	assetID   string
	mtime     int64
	windowSec float64
	targetFPS int
}

func (k videoPreviewKey) windowToken() string {
	return strconv.FormatFloat(k.windowSec, 'f', -1, 64)
}

func (k videoPreviewKey) cacheKey() string {
	return fmt.Sprintf("video:%s:%d:%s:%d", k.assetID, k.mtime, k.windowToken(), k.targetFPS)
}

func (k videoPreviewKey) diskPath(cacheDir string) string {
	return filepath.Join(
		cacheDir,
		cachePrefixDir(k.assetID),
		fmt.Sprintf("%s.video.%d.%s.%d.mp4", k.assetID, k.mtime, k.windowToken(), k.targetFPS),
	)
}

func isVideoPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".m4v", ".webm":
		return true
	default:
		return false
	}
}

func decodeVideoFrame(ctx context.Context, path string) (image.Image, error) {
	frameCtx, cancel := context.WithTimeout(ctx, videoTranscodeTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(
		frameCtx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "2",
		"-i", path,
		"-frames:v", "1",
		"-vf", "scale=w='min(2560,iw)':h='min(2560,ih)':force_original_aspect_ratio=decrease",
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: ffmpeg frame extract %s: %w (%s)", ErrMediaUnreadable, path, err, strings.TrimSpace(stderr.String()))
	}
	img, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("%w: decode extracted video frame %s: %w", ErrMediaUnreadable, path, err)
	}
	return img, nil
}

func (c *Cache) NormalizeVideoParams(windowSec float64, requestedFPS int) (float64, int) {
	if math.IsNaN(windowSec) || math.IsInf(windowSec, 0) || windowSec <= 0 {
		windowSec = defaultVideoWindowSec
	}
	if windowSec < minVideoWindowSec {
		windowSec = minVideoWindowSec
	}
	if windowSec > maxVideoWindowSec {
		windowSec = maxVideoWindowSec
	}
	windowSec = math.Round(windowSec*1000) / 1000

	targetFPS := c.videoFPS
	if requestedFPS > 0 && requestedFPS < targetFPS {
		targetFPS = requestedFPS
	}
	if targetFPS > 30 {
		targetFPS = 30
	}
	if targetFPS < 1 {
		targetFPS = 1
	}
	return windowSec, targetFPS
}

// GetOrGenerateVideoPreview returns a silent, auto-rotated, unpadded 1080p-max H.264 Preview
// capped at min(30, targetFPS, source_fps) and bounded to windowSec, keyed by (id, mtime, window, fps).
func (c *Cache) GetOrGenerateVideoPreview(
	ctx context.Context,
	assetID string,
	mtime int64,
	sourcePath string,
	windowSec float64,
	requestedFPS int,
) (VideoPreview, error) {
	normWin, targetFPS := c.NormalizeVideoParams(windowSec, requestedFPS)
	key := videoPreviewKey{
		assetID:   assetID,
		mtime:     mtime,
		windowSec: normWin,
		targetFPS: targetFPS,
	}
	cacheKey := key.cacheKey()
	targetPath := key.diskPath(c.cacheDir)

	res, err := c.getOrCreate(ctx, cacheTarget{
		assetID:    assetID,
		sourcePath: sourcePath,
		cacheKey:   cacheKey,
		targetPath: targetPath,
	}, func(genCtx context.Context) ([]byte, error) {
		return c.generateVideoPreviewBytes(genCtx, key, sourcePath)
	})
	if err != nil {
		return VideoPreview{}, err
	}

	effFPS := float64(targetFPS)
	if v, ok := c.previewFPS.Load(cacheKey); ok {
		if f, ok := v.(float64); ok && f > 0 {
			effFPS = f
		}
	} else {
		raw := res.Data
		if len(raw) == 0 && res.Path != "" {
			raw, _ = os.ReadFile(res.Path)
		}
		if len(raw) > 0 {
			effFPS = probePreviewFPS(ctx, raw, targetFPS)
			c.previewFPS.Store(cacheKey, effFPS)
		}
	}

	return VideoPreview{
		Data:      res.Data,
		Path:      res.Path,
		WindowSec: normWin,
		FPS:       effFPS,
	}, nil
}

func (c *Cache) generateVideoPreviewBytes(ctx context.Context, key videoPreviewKey, sourcePath string) ([]byte, error) {
	select {
	case c.videoSem <- struct{}{}:
		defer func() { <-c.videoSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	transCtx, cancel := context.WithTimeout(ctx, videoTranscodeTimeout)
	defer cancel()

	maxLong := max(c.videoMaxWidth, c.videoMaxHeight)
	maxShort := min(c.videoMaxWidth, c.videoMaxHeight)
	vf := fmt.Sprintf(
		"scale=w='if(gt(iw,ih),min(%d,iw),min(%d,iw))':h='if(gt(iw,ih),min(%d,ih),min(%d,ih))':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=fps='if(gt(source_fps,0),min(%d,source_fps),%d)'",
		maxLong, maxShort, maxShort, maxLong, key.targetFPS, key.targetFPS,
	)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(
		transCtx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "2",
		"-t", key.windowToken(),
		"-i", sourcePath,
		"-an",
		"-vf", vf,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "24",
		"-pix_fmt", "yuv420p",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4",
		"pipe:1",
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: ffmpeg transcode %s: %w (%s)", ErrMediaUnreadable, sourcePath, err, strings.TrimSpace(stderr.String()))
	}
	data := stdout.Bytes()
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: ffmpeg transcode %s produced empty output", ErrMediaUnreadable, sourcePath)
	}
	effFPS, packetCount, probeErr := probePreviewStream(transCtx, data, key.targetFPS)
	if probeErr != nil || packetCount == 0 {
		return nil, fmt.Errorf("%w: ffmpeg transcode %s produced unreadable or empty video stream", ErrMediaUnreadable, sourcePath)
	}
	c.previewFPS.Store(key.cacheKey(), effFPS)
	return data, nil
}

func probePreviewFPS(ctx context.Context, mp4Bytes []byte, fallbackFPS int) float64 {
	fps, _, _ := probePreviewStream(ctx, mp4Bytes, fallbackFPS)
	return fps
}

func probePreviewStream(ctx context.Context, mp4Bytes []byte, fallbackFPS int) (fps float64, packets int, err error) {
	fallback := float64(max(1, fallbackFPS))
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		probeCtx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-count_packets",
		"-show_entries", "stream=r_frame_rate,avg_frame_rate,nb_read_packets",
		"-of", "csv=p=0",
		"pipe:0",
	)
	cmd.Stdin = bytes.NewReader(mp4Bytes)
	out, err := cmd.Output()
	if err != nil {
		return fallback, 0, err
	}
	line := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if line == "" {
		return fallback, 0, errors.New("missing video stream")
	}
	tokens := strings.Split(line, ",")
	packets = -1
	if len(tokens) >= 3 {
		if n, parseErr := strconv.Atoi(strings.TrimSpace(tokens[len(tokens)-1])); parseErr == nil {
			packets = n
		}
		tokens = tokens[:len(tokens)-1]
	}
	for _, token := range tokens {
		if parsed := parseRationalFPS(token); parsed >= 1 && parsed <= float64(fallbackFPS)+0.5 {
			return math.Min(parsed, float64(fallbackFPS)), packets, nil
		}
	}
	return fallback, packets, nil
}

func parseRationalFPS(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if numStr, denStr, ok := strings.Cut(s, "/"); ok {
		num, err1 := strconv.ParseFloat(numStr, 64)
		den, err2 := strconv.ParseFloat(denStr, 64)
		if err1 == nil && err2 == nil && den > 0 && num > 0 {
			return num / den
		}
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

var errStreamWindowComplete = errors.New("stream window complete")

// StreamMJPEG paces a cached H.264 Preview as multipart/x-mixed-replace MJPEG frames
// synchronized to wall-clock ticks, looping seamlessly when sourceDurationSec < preview.WindowSec,
// and terminating immediately when ctx is canceled.
func (c *Cache) StreamMJPEG(
	ctx context.Context,
	w io.Writer,
	flush func(),
	preview VideoPreview,
	sourceDurationSec float64,
	loopShort bool,
) error {
	mp4Bytes := preview.Data
	if len(mp4Bytes) == 0 && preview.Path != "" {
		var err error
		mp4Bytes, err = os.ReadFile(preview.Path)
		if err != nil {
			return fmt.Errorf("read cached video preview: %w", err)
		}
	}
	if len(mp4Bytes) == 0 {
		return errors.New("empty video preview buffer")
	}

	effFPS := preview.FPS
	if effFPS <= 0 {
		effFPS = float64(c.videoFPS)
	}
	windowDur := time.Duration(preview.WindowSec * float64(time.Second))
	frameInterval := time.Duration(float64(time.Second) / effFPS)
	targetFrames := max(1, int(math.Round(preview.WindowSec*effFPS)))
	shouldLoop := loopShort && (sourceDurationSec <= 0 || sourceDurationSec < preview.WindowSec-0.05)

	start := time.Now()
	windowDeadline := start.Add(windowDur)
	emitted := 0

	emitFrame := func(jpegBytes []byte) error {
		if emitted >= targetFrames {
			return errStreamWindowComplete
		}
		due := start.Add(time.Duration(emitted) * frameInterval)
		if due.After(windowDeadline) {
			return errStreamWindowComplete
		}
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpegBytes)); err != nil {
			return err
		}
		if _, err := w.Write(jpegBytes); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\r\n"); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
		emitted++
		return nil
	}

	var loopFrames [][]byte
	var bufferedBytes int
	allBuffered := true

	decodePass := func(recordForLoop bool) error {
		passCtx, passCancel := context.WithCancel(ctx)
		defer passCancel()

		cmd := exec.CommandContext(
			passCtx,
			"ffmpeg",
			"-hide_banner",
			"-loglevel", "error",
			"-threads", "1",
			"-i", "pipe:0",
			"-f", "mpjpeg",
			"-boundary_tag", "frame",
			"-q:v", "5",
			"pipe:1",
		)
		cmd.Stdin = bytes.NewReader(mp4Bytes)
		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start ffmpeg mjpeg emitter: %w", err)
		}
		defer func() {
			passCancel()
			_ = stdoutPipe.Close()
			_ = cmd.Wait()
		}()

		mr := multipart.NewReader(stdoutPipe, "frame")
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("read mjpeg frame: %w (%s)", err, strings.TrimSpace(stderr.String()))
			}
			jpegBytes, readErr := io.ReadAll(part)
			_ = part.Close()
			if readErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return readErr
			}
			if len(jpegBytes) == 0 {
				continue
			}
			if recordForLoop {
				if bufferedBytes+len(jpegBytes) <= maxLoopFrameBuffer {
					loopFrames = append(loopFrames, jpegBytes)
					bufferedBytes += len(jpegBytes)
				} else {
					allBuffered = false
				}
			}
			if err := emitFrame(jpegBytes); err != nil {
				return err
			}
			if emitted >= targetFrames {
				return errStreamWindowComplete
			}
		}
	}

	err := decodePass(shouldLoop)
	if err != nil && !errors.Is(err, errStreamWindowComplete) {
		return err
	}

	if shouldLoop && emitted > 0 && emitted < targetFrames && !errors.Is(err, errStreamWindowComplete) {
		if allBuffered && len(loopFrames) > 0 {
			idx := 0
			for emitted < targetFrames {
				if err := emitFrame(loopFrames[idx%len(loopFrames)]); err != nil {
					if errors.Is(err, errStreamWindowComplete) {
						break
					}
					return err
				}
				idx++
			}
		} else {
			for emitted < targetFrames {
				before := emitted
				if passErr := decodePass(false); passErr != nil {
					if errors.Is(passErr, errStreamWindowComplete) {
						break
					}
					return passErr
				}
				if emitted == before {
					break
				}
			}
		}
	}

	if rem := time.Until(windowDeadline); rem > 0 {
		timer := time.NewTimer(rem)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	if _, err := io.WriteString(w, "--frame--\r\n"); err != nil {
		return err
	}
	if flush != nil {
		flush()
	}
	return nil
}
