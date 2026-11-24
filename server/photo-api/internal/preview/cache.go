package preview

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/singleflight"
)

type Options struct {
	Mode                        string // "memory", "disk", "hybrid" (default: "memory")
	MaxWidth                    int
	MaxHeight                   int
	JPEGQuality                 int
	MaxMB                       int64
	PrebakeBlur                 bool
	FrameWidth                  int
	FrameHeight                 int
	MaxConcurrentDecodes        int
	VideoMaxWidth               int
	VideoMaxHeight              int
	VideoFPS                    int
	MaxConcurrentVideoTranscode int
}

type Result struct {
	Data []byte
	Path string
}

type VideoClipResult struct {
	Data      []byte
	Path      string
	WindowSec float64
	FPS       float64
}

type lruEntry struct {
	key       string
	data      []byte
	size      int64
	updatedAt time.Time
}

type MemoryLRU struct {
	mu        sync.Mutex
	maxBytes  int64
	curBytes  int64
	items     map[string]*list.Element
	evictList *list.List
}

func NewMemoryLRU(maxBytes int64) *MemoryLRU {
	return &MemoryLRU{
		maxBytes:  maxBytes,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

func (m *MemoryLRU) Get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	elem, ok := m.items[key]
	if !ok {
		return nil, false
	}
	m.evictList.MoveToFront(elem)
	entry := elem.Value.(*lruEntry)
	entry.updatedAt = time.Now()
	return entry.data, true
}

func (m *MemoryLRU) Put(key string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	size := int64(len(data))
	if elem, ok := m.items[key]; ok {
		m.evictList.MoveToFront(elem)
		entry := elem.Value.(*lruEntry)
		m.curBytes += size - entry.size
		entry.data = data
		entry.size = size
		entry.updatedAt = time.Now()
	} else {
		entry := &lruEntry{
			key:       key,
			data:      data,
			size:      size,
			updatedAt: time.Now(),
		}
		elem := m.evictList.PushFront(entry)
		m.items[key] = elem
		m.curBytes += size
	}

	for m.curBytes > m.maxBytes && m.evictList.Len() > 0 {
		oldest := m.evictList.Back()
		if oldest == nil {
			break
		}
		m.evictList.Remove(oldest)
		oldEntry := oldest.Value.(*lruEntry)
		delete(m.items, oldEntry.key)
		m.curBytes -= oldEntry.size
	}
}

func (m *MemoryLRU) Remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if elem, ok := m.items[key]; ok {
		m.evictList.Remove(elem)
		entry := elem.Value.(*lruEntry)
		delete(m.items, key)
		m.curBytes -= entry.size
	}
}

func (m *MemoryLRU) RemovePrefix(prefix string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, elem := range m.items {
		if strings.HasPrefix(key, prefix) {
			m.evictList.Remove(elem)
			entry := elem.Value.(*lruEntry)
			delete(m.items, key)
			m.curBytes -= entry.size
		}
	}
}

func (m *MemoryLRU) Stats() (count int, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items), m.curBytes
}

type Cache struct {
	mode           string
	cacheDir       string
	maxWidth       int
	maxHeight      int
	jpegQuality    int
	maxBytes       int64
	prebakeBlur    bool
	frameWidth     int
	frameHeight    int
	videoMaxWidth  int
	videoMaxHeight int
	videoFPS       int

	memLRU   *MemoryLRU
	sem      chan struct{}
	videoSem chan struct{}
	clipFPS  sync.Map
	sf       singleflight.Group
	evictMu  sync.Mutex
}

func NewCache(dataDir string, opts Options) (*Cache, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = "memory"
	}
	if mode != "memory" && mode != "disk" && mode != "hybrid" {
		mode = "memory"
	}

	cacheDir := filepath.Join(dataDir, "cache")
	if mode != "memory" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create preview cache dir %s: %w", cacheDir, err)
		}
	}

	if opts.MaxWidth <= 0 {
		opts.MaxWidth = 2560
	}
	if opts.MaxHeight <= 0 {
		opts.MaxHeight = 2560
	}
	if opts.JPEGQuality <= 0 || opts.JPEGQuality > 100 {
		opts.JPEGQuality = 88
	}
	if opts.MaxMB <= 0 {
		opts.MaxMB = 1024
	}
	if opts.FrameWidth <= 0 {
		opts.FrameWidth = 1440
	}
	if opts.FrameHeight <= 0 {
		opts.FrameHeight = 2560
	}
	maxConcurrent := opts.MaxConcurrentDecodes
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	if opts.VideoMaxWidth <= 0 {
		opts.VideoMaxWidth = 1920
	}
	if opts.VideoMaxHeight <= 0 {
		opts.VideoMaxHeight = 1080
	}
	if opts.VideoFPS <= 0 {
		opts.VideoFPS = 30
	}
	if opts.VideoFPS > 30 {
		opts.VideoFPS = 30
	}
	maxVideoTranscode := opts.MaxConcurrentVideoTranscode
	if maxVideoTranscode <= 0 {
		maxVideoTranscode = 1
	}

	maxBytes := opts.MaxMB * 1024 * 1024
	return &Cache{
		mode:           mode,
		cacheDir:       cacheDir,
		maxWidth:       opts.MaxWidth,
		maxHeight:      opts.MaxHeight,
		jpegQuality:    opts.JPEGQuality,
		maxBytes:       maxBytes,
		prebakeBlur:    opts.PrebakeBlur,
		frameWidth:     opts.FrameWidth,
		frameHeight:    opts.FrameHeight,
		videoMaxWidth:  opts.VideoMaxWidth,
		videoMaxHeight: opts.VideoMaxHeight,
		videoFPS:       opts.VideoFPS,
		memLRU:         NewMemoryLRU(maxBytes),
		sem:            make(chan struct{}, maxConcurrent),
		videoSem:       make(chan struct{}, maxVideoTranscode),
	}, nil
}

type RenditionKind struct {
	keyPrefix  string
	etagPrefix string
	fileSuffix string
}

var (
	PreviewKind  = RenditionKind{keyPrefix: "", etagPrefix: "", fileSuffix: ".jpg"}
	BackdropKind = RenditionKind{keyPrefix: "backdrop:", etagPrefix: "backdrop-", fileSuffix: ".backdrop.jpg"}
)

func (k RenditionKind) CacheKey(assetID string) string {
	return k.keyPrefix + assetID
}

func (k RenditionKind) ETag(assetID string, mtime int64) string {
	return fmt.Sprintf(`"%s%s-%d"`, k.etagPrefix, assetID, mtime)
}

func (k RenditionKind) Filename(assetID string) string {
	return assetID + k.fileSuffix
}

func (c *Cache) cachePath(assetID string) string {
	return c.kindPath(assetID, PreviewKind)
}

func (c *Cache) backdropCachePath(assetID string) string {
	return c.kindPath(assetID, BackdropKind)
}

func (c *Cache) kindPath(assetID string, kind RenditionKind) string {
	prefix := "00"
	if len(assetID) >= 2 {
		prefix = assetID[:2]
	}
	return filepath.Join(c.cacheDir, prefix, kind.Filename(assetID))
}

// GetOrGenerate returns the preview JPEG for the asset (either in-memory bytes or disk file path).
func (c *Cache) GetOrGenerate(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(PreviewKind.CacheKey(assetID), c.cachePath(assetID), func() ([]byte, error) {
		return c.generateBytes(ctx, sourcePath, orientation)
	})
}

// GetOrGenerateBackdrop returns a compact, pre-blurred, ~35%-darkened JPEG backdrop for the asset.
func (c *Cache) GetOrGenerateBackdrop(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(BackdropKind.CacheKey(assetID), c.backdropCachePath(assetID), func() ([]byte, error) {
		return c.generateBackdropBytes(ctx, sourcePath, orientation)
	})
}

func (c *Cache) getOrCreate(cacheKey, targetPath string, generate func() ([]byte, error)) (Result, error) {
	switch c.mode {
	case "disk":
		return c.getOrGenerateDisk(cacheKey, targetPath, generate)
	case "hybrid":
		return c.getOrGenerateHybrid(cacheKey, targetPath, generate)
	default:
		return c.getOrGenerateMemory(cacheKey, generate)
	}
}

func (c *Cache) getOrGenerateMemory(cacheKey string, generate func() ([]byte, error)) (Result, error) {
	if data, ok := c.memLRU.Get(cacheKey); ok && len(data) > 0 {
		return Result{Data: data}, nil
	}

	val, err, _ := c.sf.Do(cacheKey, func() (any, error) {
		if data, ok := c.memLRU.Get(cacheKey); ok && len(data) > 0 {
			return data, nil
		}
		data, err := generate()
		if err != nil {
			return nil, err
		}
		c.memLRU.Put(cacheKey, data)
		return data, nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Data: val.([]byte)}, nil
}

func (c *Cache) getOrGenerateDisk(cacheKey, target string, generate func() ([]byte, error)) (Result, error) {
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		now := time.Now()
		_ = os.Chtimes(target, now, now)
		return Result{Path: target}, nil
	}

	val, err, _ := c.sf.Do(cacheKey, func() (any, error) {
		if info, err := os.Stat(target); err == nil && info.Size() > 0 {
			now := time.Now()
			_ = os.Chtimes(target, now, now)
			return target, nil
		}
		data, err := generate()
		if err != nil {
			return "", err
		}
		if err := c.writeToDisk(target, data); err != nil {
			return "", err
		}
		c.enforceDiskLimit(target)
		return target, nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Path: val.(string)}, nil
}

func (c *Cache) getOrGenerateHybrid(cacheKey, target string, generate func() ([]byte, error)) (Result, error) {
	if data, ok := c.memLRU.Get(cacheKey); ok && len(data) > 0 {
		return Result{Data: data}, nil
	}

	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		data, err := os.ReadFile(target)
		if err == nil && len(data) > 0 {
			c.memLRU.Put(cacheKey, data)
			now := time.Now()
			_ = os.Chtimes(target, now, now)
			return Result{Data: data, Path: target}, nil
		}
	}

	val, err, _ := c.sf.Do(cacheKey, func() (any, error) {
		if data, ok := c.memLRU.Get(cacheKey); ok && len(data) > 0 {
			return data, nil
		}
		data, err := generate()
		if err != nil {
			return nil, err
		}
		c.memLRU.Put(cacheKey, data)
		_ = c.writeToDisk(target, data)
		c.enforceDiskLimit(target)
		return data, nil
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Data: val.([]byte), Path: target}, nil
}

// Evict removes cached renditions (preview, backdrop, and video clips) from memory and disk if present.
func (c *Cache) Evict(assetID string) {
	c.memLRU.Remove(PreviewKind.CacheKey(assetID))
	c.memLRU.Remove(BackdropKind.CacheKey(assetID))
	c.memLRU.RemovePrefix("video:" + assetID + ":")
	c.clipFPS.Range(func(k, _ any) bool {
		if ks, ok := k.(string); ok && strings.HasPrefix(ks, "video:"+assetID+":") {
			c.clipFPS.Delete(k)
		}
		return true
	})
	_ = os.Remove(c.cachePath(assetID))
	_ = os.Remove(c.backdropCachePath(assetID))
	if c.mode != "memory" {
		prefix := "00"
		if len(assetID) >= 2 {
			prefix = assetID[:2]
		}
		matches, _ := filepath.Glob(filepath.Join(c.cacheDir, prefix, assetID+".video.*.mp4"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}

func isVideoPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".m4v", ".webm":
		return true
	default:
		return false
	}
}

func (c *Cache) decodeOrientedImage(ctx context.Context, sourcePath string, orientation int) (image.Image, error) {
	if isVideoPath(sourcePath) {
		select {
		case c.videoSem <- struct{}{}:
			defer func() { <-c.videoSem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return decodeVideoFrame(ctx, sourcePath)
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	img, err := decodeImage(ctx, sourcePath)
	if err != nil {
		return nil, err
	}
	if orientation > 1 && orientation <= 8 {
		img = applyOrientation(img, orientation)
	}
	return img, nil
}

func (c *Cache) generateBytes(ctx context.Context, sourcePath string, orientation int) ([]byte, error) {
	img, err := c.decodeOrientedImage(ctx, sourcePath, orientation)
	if err != nil {
		return nil, err
	}

	if c.prebakeBlur {
		img = compositeBlurredFrame(img, c.frameWidth, c.frameHeight)
	} else {
		img = fitWithin(img, c.maxWidth, c.maxHeight)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: c.jpegQuality}); err != nil {
		return nil, fmt.Errorf("encode preview jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

const (
	backdropMaxEdge = 640
	backdropBlurRad = 20
	backdropBlurOps = 3
	backdropDimKeep = 0.65 // ~35% darkened
	backdropQuality = 80
)

func (c *Cache) generateBackdropBytes(ctx context.Context, sourcePath string, orientation int) ([]byte, error) {
	img, err := c.decodeOrientedImage(ctx, sourcePath, orientation)
	if err != nil {
		return nil, err
	}

	dstW, dstH := backdropDimensions(c.frameWidth, c.frameHeight, backdropMaxEdge)
	backdrop := renderBlurredBackdrop(img, dstW, dstH, backdropBlurRad, backdropBlurOps, backdropDimKeep)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, backdrop, &jpeg.Options{Quality: backdropQuality}); err != nil {
		return nil, fmt.Errorf("encode backdrop jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

func (c *Cache) writeToDisk(targetPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, targetPath)
}

func decodeImage(ctx context.Context, path string) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".heic" || ext == ".heif" {
		return decodeHEIC(ctx, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

func decodeHEIC(ctx context.Context, path string) (image.Image, error) {
	tmpFile, err := os.CreateTemp("", "heif-*.jpg")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, "heif-convert", "-q", "92", path, tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("heif-convert %s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

const (
	defaultVideoWindowSec = 12.0
	minVideoWindowSec     = 0.5
	maxVideoWindowSec     = 300.0
	videoTranscodeTimeout = 60 * time.Second
	maxLoopFrameBuffer    = 32 * 1024 * 1024
)

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
		return nil, fmt.Errorf("ffmpeg frame extract %s: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	img, _, err := image.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("decode extracted video frame %s: %w", path, err)
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

func formatWindowKey(windowSec float64) string {
	return strconv.FormatFloat(windowSec, 'f', -1, 64)
}

func videoCacheKey(assetID string, mtime int64, windowKey string, targetFPS int) string {
	return fmt.Sprintf("video:%s:%d:%s:%d", assetID, mtime, windowKey, targetFPS)
}

func (c *Cache) videoCachePath(assetID string, mtime int64, windowKey string, targetFPS int) string {
	prefix := "00"
	if len(assetID) >= 2 {
		prefix = assetID[:2]
	}
	return filepath.Join(c.cacheDir, prefix, fmt.Sprintf("%s.video.%d.%s.%d.mp4", assetID, mtime, windowKey, targetFPS))
}

// GetOrGenerateVideoClip returns a silent, auto-rotated, unpadded 1080p-max H.264 MP4 clip
// capped at min(30, targetFPS, source_fps) and trimmed to windowSec, keyed by (id, mtime, window, fps).
func (c *Cache) GetOrGenerateVideoClip(
	ctx context.Context,
	assetID string,
	mtime int64,
	sourcePath string,
	windowSec float64,
	requestedFPS int,
) (VideoClipResult, error) {
	normWin, targetFPS := c.NormalizeVideoParams(windowSec, requestedFPS)
	winKey := formatWindowKey(normWin)
	cacheKey := videoCacheKey(assetID, mtime, winKey, targetFPS)
	targetPath := c.videoCachePath(assetID, mtime, winKey, targetFPS)

	res, err := c.getOrCreate(cacheKey, targetPath, func() ([]byte, error) {
		return c.generateVideoClipBytes(ctx, cacheKey, sourcePath, normWin, targetFPS)
	})
	if err != nil {
		return VideoClipResult{}, err
	}

	effFPS := float64(targetFPS)
	if v, ok := c.clipFPS.Load(cacheKey); ok {
		if f, ok := v.(float64); ok && f > 0 {
			effFPS = f
		}
	} else {
		raw := res.Data
		if len(raw) == 0 && res.Path != "" {
			raw, _ = os.ReadFile(res.Path)
		}
		if len(raw) > 0 {
			effFPS = probeClipFPS(ctx, raw, targetFPS)
			c.clipFPS.Store(cacheKey, effFPS)
		}
	}

	return VideoClipResult{
		Data:      res.Data,
		Path:      res.Path,
		WindowSec: normWin,
		FPS:       effFPS,
	}, nil
}

func (c *Cache) generateVideoClipBytes(ctx context.Context, cacheKey, sourcePath string, windowSec float64, targetFPS int) ([]byte, error) {
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
		maxLong, maxShort, maxShort, maxLong, targetFPS, targetFPS,
	)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(
		transCtx,
		"ffmpeg",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "2",
		"-t", formatWindowKey(windowSec),
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
		return nil, fmt.Errorf("ffmpeg transcode %s: %w (%s)", sourcePath, err, strings.TrimSpace(stderr.String()))
	}
	data := stdout.Bytes()
	if len(data) == 0 {
		return nil, fmt.Errorf("ffmpeg transcode %s produced empty output", sourcePath)
	}
	c.clipFPS.Store(cacheKey, probeClipFPS(transCtx, data, targetFPS))
	return data, nil
}

func probeClipFPS(ctx context.Context, mp4Bytes []byte, fallbackFPS int) float64 {
	fallback := float64(max(1, fallbackFPS))
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		probeCtx,
		"ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate,avg_frame_rate",
		"-of", "csv=p=0",
		"pipe:0",
	)
	cmd.Stdin = bytes.NewReader(mp4Bytes)
	out, err := cmd.Output()
	if err != nil {
		return fallback
	}
	line := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	for _, token := range strings.Split(line, ",") {
		if fps := parseRationalFPS(token); fps >= 1 && fps <= float64(fallbackFPS)+0.5 {
			return math.Min(fps, float64(fallbackFPS))
		}
	}
	return fallback
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

// StreamMJPEG paces a cached H.264 MP4 clip as multipart/x-mixed-replace MJPEG frames
// synchronized to wall-clock ticks, looping seamlessly when sourceDurationSec < clip.WindowSec,
// and terminating immediately when ctx is canceled.
func (c *Cache) StreamMJPEG(
	ctx context.Context,
	w io.Writer,
	flush func(),
	clip VideoClipResult,
	sourceDurationSec float64,
) error {
	mp4Bytes := clip.Data
	if len(mp4Bytes) == 0 && clip.Path != "" {
		var err error
		mp4Bytes, err = os.ReadFile(clip.Path)
		if err != nil {
			return fmt.Errorf("read cached video clip: %w", err)
		}
	}
	if len(mp4Bytes) == 0 {
		return errors.New("empty video clip buffer")
	}

	effFPS := clip.FPS
	if effFPS <= 0 {
		effFPS = float64(c.videoFPS)
	}
	windowDur := time.Duration(clip.WindowSec * float64(time.Second))
	frameInterval := time.Duration(float64(time.Second) / effFPS)
	targetFrames := max(1, int(math.Round(clip.WindowSec*effFPS)))
	shouldLoop := sourceDurationSec <= 0 || sourceDurationSec < clip.WindowSec-0.05

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

func fitWithin(src image.Image, maxW, maxH int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW && h <= maxH {
		return src
	}

	scaleW := float64(maxW) / float64(w)
	scaleH := float64(maxH) / float64(h)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	dstW := int(float64(w)*scale + 0.5)
	dstH := int(float64(h)*scale + 0.5)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func backdropDimensions(frameW, frameH, maxEdge int) (int, int) {
	if frameW <= 0 {
		frameW = 1440
	}
	if frameH <= 0 {
		frameH = 2560
	}
	if maxEdge <= 0 {
		maxEdge = 640
	}
	scale := float64(maxEdge) / float64(max(frameW, frameH))
	if scale > 1 {
		scale = 1
	}
	dstW := max(16, int(float64(frameW)*scale+0.5))
	dstH := max(16, int(float64(frameH)*scale+0.5))
	return dstW, dstH
}

func coverCropRect(b image.Rectangle, targetW, targetH int) image.Rectangle {
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 || targetW <= 0 || targetH <= 0 {
		return b
	}
	scaleCover := float64(targetW) / float64(srcW)
	if sH := float64(targetH) / float64(srcH); sH > scaleCover {
		scaleCover = sH
	}
	cropW := int(float64(targetW)/scaleCover + 0.5)
	cropH := int(float64(targetH)/scaleCover + 0.5)
	cropX := b.Min.X + (srcW-cropW)/2
	cropY := b.Min.Y + (srcH-cropH)/2
	return image.Rect(cropX, cropY, cropX+cropW, cropY+cropH).Intersect(b)
}

func renderBlurredBackdrop(src image.Image, dstW, dstH, radius, passes int, dim float64) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return dst
	}
	cropRect := coverCropRect(b, dstW, dstH)
	draw.BiLinear.Scale(dst, dst.Bounds(), src, cropRect, draw.Src, nil)
	return separableBoxBlurAndDim(dst, radius, passes, dim)
}

func boxBlur1D(dstPix, srcPix []uint8, lines, lineLen, lineStride, stepStride, radius int) {
	win := 2*radius + 1
	for line := 0; line < lines; line++ {
		baseOff := line * lineStride
		var rSum, gSum, bSum int
		for k := -radius; k <= radius; k++ {
			ck := min(lineLen-1, max(0, k))
			off := baseOff + ck*stepStride
			rSum += int(srcPix[off])
			gSum += int(srcPix[off+1])
			bSum += int(srcPix[off+2])
		}
		for pos := 0; pos < lineLen; pos++ {
			dOff := baseOff + pos*stepStride
			dstPix[dOff] = uint8(rSum / win)
			dstPix[dOff+1] = uint8(gSum / win)
			dstPix[dOff+2] = uint8(bSum / win)
			dstPix[dOff+3] = 255

			outOff := baseOff + max(0, pos-radius)*stepStride
			inOff := baseOff + min(lineLen-1, pos+radius+1)*stepStride
			rSum += int(srcPix[inOff]) - int(srcPix[outOff])
			gSum += int(srcPix[inOff+1]) - int(srcPix[outOff+1])
			bSum += int(srcPix[inOff+2]) - int(srcPix[outOff+2])
		}
	}
}

func separableBoxBlurAndDim(src *image.RGBA, radius, passes int, dim float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || radius <= 0 || passes <= 0 {
		return src
	}

	cur := src
	tmp := image.NewRGBA(b)
	next := image.NewRGBA(b)

	for pass := 0; pass < passes; pass++ {
		boxBlur1D(tmp.Pix, cur.Pix, h, w, cur.Stride, 4, radius)
		boxBlur1D(next.Pix, tmp.Pix, w, h, 4, tmp.Stride, radius)
		cur, next = next, cur
	}

	for i := 0; i < len(cur.Pix); i += 4 {
		cur.Pix[i] = uint8(float64(cur.Pix[i])*dim + 0.5)
		cur.Pix[i+1] = uint8(float64(cur.Pix[i+1])*dim + 0.5)
		cur.Pix[i+2] = uint8(float64(cur.Pix[i+2])*dim + 0.5)
	}
	return cur
}

func compositeBlurredFrame(src image.Image, frameW, frameH int) image.Image {
	b := src.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW <= 0 || srcH <= 0 {
		return src
	}

	smallW := 64
	smallH := max(16, int(float64(smallW)*float64(frameH)/float64(frameW)+0.5))

	cropRect := coverCropRect(b, frameW, frameH)

	small := image.NewRGBA(image.Rect(0, 0, smallW, smallH))
	draw.BiLinear.Scale(small, small.Bounds(), src, cropRect, draw.Src, nil)
	small = separableBoxBlurAndDim(small, 3, 2, 0.72)

	canvas := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	draw.BiLinear.Scale(canvas, canvas.Bounds(), small, small.Bounds(), draw.Src, nil)

	scaleContain := float64(frameW) / float64(srcW)
	if sH := float64(frameH) / float64(srcH); sH < scaleContain {
		scaleContain = sH
	}
	fgW := max(1, int(float64(srcW)*scaleContain+0.5))
	fgH := max(1, int(float64(srcH)*scaleContain+0.5))
	offsetX := (frameW - fgW) / 2
	offsetY := (frameH - fgH) / 2
	fgRect := image.Rect(offsetX, offsetY, offsetX+fgW, offsetY+fgH)
	draw.CatmullRom.Scale(canvas, fgRect, src, b, draw.Src, nil)
	return canvas
}

// applyOrientation transforms an image according to EXIF orientation tags 2-8.
func applyOrientation(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	dstW, dstH := w, h
	if orientation >= 5 && orientation <= 8 {
		dstW, dstH = h, w
	}

	var mapCoord func(x, y int) (int, int)
	switch orientation {
	case 2:
		mapCoord = func(x, y int) (int, int) { return w - 1 - x, y }
	case 3:
		mapCoord = func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }
	case 4:
		mapCoord = func(x, y int) (int, int) { return x, h - 1 - y }
	case 5:
		mapCoord = func(x, y int) (int, int) { return y, x }
	case 6:
		mapCoord = func(x, y int) (int, int) { return h - 1 - y, x }
	case 7:
		mapCoord = func(x, y int) (int, int) { return h - 1 - y, w - 1 - x }
	case 8:
		mapCoord = func(x, y int) (int, int) { return y, w - 1 - x }
	default:
		return src
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := mapCoord(x, y)
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

type cachedFile struct {
	path  string
	size  int64
	mtime time.Time
}

func (c *Cache) enforceDiskLimit(keepPath string) {
	c.evictMu.Lock()
	defer c.evictMu.Unlock()

	var files []cachedFile
	var total int64

	_ = filepath.WalkDir(c.cacheDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(d.Name(), ".jpg") && !strings.HasSuffix(d.Name(), ".mp4")) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		files = append(files, cachedFile{
			path:  path,
			size:  info.Size(),
			mtime: info.ModTime(),
		})
		return nil
	})

	if total <= c.maxBytes {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.Before(files[j].mtime)
	})

	for _, f := range files {
		if total <= c.maxBytes {
			break
		}
		if f.path == keepPath {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
}
