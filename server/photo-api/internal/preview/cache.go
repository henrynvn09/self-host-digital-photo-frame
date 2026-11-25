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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

const defaultNegativeCacheTTL = 10 * time.Minute

// ErrMediaUnreadable indicates that a source Asset is missing, unreadable, truncated, or corrupt.
var ErrMediaUnreadable = errors.New("media asset unreadable or corrupt")

type negCacheEntry struct {
	errMsg    string
	expiresAt time.Time
}

type Result struct {
	Data []byte
	Path string
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

	memLRU     *MemoryLRU
	sem        chan struct{}
	videoSem   chan struct{}
	previewFPS sync.Map
	sf         singleflight.Group
	evictMu    sync.Mutex
	negMu      sync.Mutex
	negItems   map[string]negCacheEntry
	negTTL     time.Duration
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
		negItems:       make(map[string]negCacheEntry),
		negTTL:         defaultNegativeCacheTTL,
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

func cachePrefixDir(assetID string) string {
	if len(assetID) >= 2 {
		return assetID[:2]
	}
	return "00"
}

func (c *Cache) cachePath(assetID string) string {
	return c.kindPath(assetID, PreviewKind)
}

func (c *Cache) backdropCachePath(assetID string) string {
	return c.kindPath(assetID, BackdropKind)
}

func (c *Cache) kindPath(assetID string, kind RenditionKind) string {
	return filepath.Join(c.cacheDir, cachePrefixDir(assetID), kind.Filename(assetID))
}

type cacheTarget struct {
	assetID    string
	sourcePath string
	cacheKey   string
	targetPath string
}

// GetOrGenerate returns the preview JPEG for the asset (either in-memory bytes or disk file path).
func (c *Cache) GetOrGenerate(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(ctx, cacheTarget{
		assetID:    assetID,
		sourcePath: sourcePath,
		cacheKey:   PreviewKind.CacheKey(assetID),
		targetPath: c.cachePath(assetID),
	}, func(genCtx context.Context) ([]byte, error) {
		return c.generateBytes(genCtx, sourcePath, orientation)
	})
}

// GetOrGenerateBackdrop returns a compact, pre-blurred, ~35%-darkened JPEG backdrop for the asset.
func (c *Cache) GetOrGenerateBackdrop(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(ctx, cacheTarget{
		assetID:    assetID,
		sourcePath: sourcePath,
		cacheKey:   BackdropKind.CacheKey(assetID),
		targetPath: c.backdropCachePath(assetID),
	}, func(genCtx context.Context) ([]byte, error) {
		return c.generateBackdropBytes(genCtx, sourcePath, orientation)
	})
}

func (c *Cache) negativeKey(assetID, sourcePath string) string {
	if info, err := os.Stat(sourcePath); err == nil {
		return fmt.Sprintf("neg:%s:%d:%d", assetID, info.ModTime().UnixNano(), info.Size())
	}
	return fmt.Sprintf("neg:%s:missing", assetID)
}

func (c *Cache) checkNegative(assetID, sourcePath string) error {
	if assetID == "" || sourcePath == "" {
		return nil
	}
	key := c.negativeKey(assetID, sourcePath)
	now := time.Now()

	c.negMu.Lock()
	defer c.negMu.Unlock()
	entry, ok := c.negItems[key]
	if !ok {
		return nil
	}
	if !now.Before(entry.expiresAt) {
		delete(c.negItems, key)
		return nil
	}
	return fmt.Errorf("%w: %s (cached)", ErrMediaUnreadable, entry.errMsg)
}

func (c *Cache) recordNegative(assetID, sourcePath string, err error) {
	if assetID == "" || sourcePath == "" || err == nil {
		return
	}
	key := c.negativeKey(assetID, sourcePath)
	now := time.Now()

	c.negMu.Lock()
	defer c.negMu.Unlock()
	if len(c.negItems) >= 256 {
		for k, v := range c.negItems {
			if !now.Before(v.expiresAt) {
				delete(c.negItems, k)
			}
		}
	}
	c.negItems[key] = negCacheEntry{
		errMsg:    err.Error(),
		expiresAt: now.Add(c.negTTL),
	}
}

func (c *Cache) evictNegative(assetID string) {
	prefix := "neg:" + assetID + ":"
	c.negMu.Lock()
	defer c.negMu.Unlock()
	for k := range c.negItems {
		if strings.HasPrefix(k, prefix) {
			delete(c.negItems, k)
		}
	}
}

func (c *Cache) waitSingleflight(ctx context.Context, cacheKey string, fn func() (Result, error)) (Result, error) {
	ch := c.sf.DoChan(cacheKey, func() (any, error) {
		return fn()
	})
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return Result{}, res.Err
		}
		return res.Val.(Result), nil
	}
}

func (c *Cache) runDetachedGenerate(assetID, sourcePath string, generate func(genCtx context.Context) ([]byte, error)) ([]byte, error) {
	if negErr := c.checkNegative(assetID, sourcePath); negErr != nil {
		return nil, negErr
	}
	genCtx, cancel := context.WithTimeout(context.Background(), videoTranscodeTimeout)
	defer cancel()
	data, err := generate(genCtx)
	if err != nil {
		if errors.Is(err, ErrMediaUnreadable) {
			c.recordNegative(assetID, sourcePath, err)
		}
		return nil, err
	}
	return data, nil
}

func (c *Cache) lookupCached(t cacheTarget) (Result, bool) {
	switch c.mode {
	case "disk":
		if info, err := os.Stat(t.targetPath); err == nil && info.Size() > 0 {
			now := time.Now()
			_ = os.Chtimes(t.targetPath, now, now)
			return Result{Path: t.targetPath}, true
		}
		return Result{}, false
	case "hybrid":
		if data, ok := c.memLRU.Get(t.cacheKey); ok && len(data) > 0 {
			return Result{Data: data, Path: t.targetPath}, true
		}
		if info, err := os.Stat(t.targetPath); err == nil && info.Size() > 0 {
			if data, err := os.ReadFile(t.targetPath); err == nil && len(data) > 0 {
				c.memLRU.Put(t.cacheKey, data)
				now := time.Now()
				_ = os.Chtimes(t.targetPath, now, now)
				return Result{Data: data, Path: t.targetPath}, true
			}
		}
		return Result{}, false
	default:
		if data, ok := c.memLRU.Get(t.cacheKey); ok && len(data) > 0 {
			return Result{Data: data}, true
		}
		return Result{}, false
	}
}

func (c *Cache) storeCached(t cacheTarget, data []byte) (Result, error) {
	switch c.mode {
	case "disk":
		if err := c.writeToDisk(t.targetPath, data); err != nil {
			return Result{}, err
		}
		c.enforceDiskLimit(t.targetPath)
		return Result{Path: t.targetPath}, nil
	case "hybrid":
		c.memLRU.Put(t.cacheKey, data)
		_ = c.writeToDisk(t.targetPath, data)
		c.enforceDiskLimit(t.targetPath)
		return Result{Data: data, Path: t.targetPath}, nil
	default:
		c.memLRU.Put(t.cacheKey, data)
		return Result{Data: data}, nil
	}
}

func (c *Cache) getOrCreate(
	ctx context.Context,
	t cacheTarget,
	generate func(genCtx context.Context) ([]byte, error),
) (Result, error) {
	if res, ok := c.lookupCached(t); ok {
		return res, nil
	}
	if negErr := c.checkNegative(t.assetID, t.sourcePath); negErr != nil {
		return Result{}, negErr
	}

	return c.waitSingleflight(ctx, t.cacheKey, func() (Result, error) {
		if res, ok := c.lookupCached(t); ok {
			return res, nil
		}
		data, err := c.runDetachedGenerate(t.assetID, t.sourcePath, generate)
		if err != nil {
			return Result{}, err
		}
		return c.storeCached(t, data)
	})
}

// Evict removes cached renditions (photo/poster preview, backdrop, video previews, and negative-cache entries) from memory and disk if present.
func (c *Cache) Evict(assetID string) {
	c.memLRU.Remove(PreviewKind.CacheKey(assetID))
	c.memLRU.Remove(BackdropKind.CacheKey(assetID))
	c.memLRU.RemovePrefix("video:" + assetID + ":")
	c.evictNegative(assetID)
	c.previewFPS.Range(func(k, _ any) bool {
		if ks, ok := k.(string); ok && strings.HasPrefix(ks, "video:"+assetID+":") {
			c.previewFPS.Delete(k)
		}
		return true
	})
	_ = os.Remove(c.cachePath(assetID))
	_ = os.Remove(c.backdropCachePath(assetID))
	if c.mode != "memory" {
		matches, _ := filepath.Glob(filepath.Join(c.cacheDir, cachePrefixDir(assetID), assetID+".video.*.mp4"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
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
		return nil, fmt.Errorf("%w: open %s: %w", ErrMediaUnreadable, path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%w: decode %s: %w", ErrMediaUnreadable, path, err)
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
		return nil, fmt.Errorf("%w: heif-convert %s: %w (%s)", ErrMediaUnreadable, path, err, strings.TrimSpace(string(out)))
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%w: decode converted heic %s: %w", ErrMediaUnreadable, path, err)
	}
	return img, nil
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
