package preview

import (
	"bytes"
	"container/list"
	"context"
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
	Mode                 string // "memory", "disk", "hybrid" (default: "memory")
	MaxWidth             int
	MaxHeight            int
	JPEGQuality          int
	MaxMB                int64
	PrebakeBlur          bool
	FrameWidth           int
	FrameHeight          int
	MaxConcurrentDecodes int
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

func (m *MemoryLRU) Stats() (count int, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items), m.curBytes
}

type Cache struct {
	mode        string
	cacheDir    string
	maxWidth    int
	maxHeight   int
	jpegQuality int
	maxBytes    int64
	prebakeBlur bool
	frameWidth  int
	frameHeight int

	memLRU  *MemoryLRU
	sem     chan struct{}
	sf      singleflight.Group
	evictMu sync.Mutex
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

	maxBytes := opts.MaxMB * 1024 * 1024
	return &Cache{
		mode:        mode,
		cacheDir:    cacheDir,
		maxWidth:    opts.MaxWidth,
		maxHeight:   opts.MaxHeight,
		jpegQuality: opts.JPEGQuality,
		maxBytes:    maxBytes,
		prebakeBlur: opts.PrebakeBlur,
		frameWidth:  opts.FrameWidth,
		frameHeight: opts.FrameHeight,
		memLRU:      NewMemoryLRU(maxBytes),
		sem:         make(chan struct{}, maxConcurrent),
	}, nil
}

func backdropCacheKey(assetID string) string {
	return "backdrop:" + assetID
}

func (c *Cache) cachePath(assetID string) string {
	return c.cacheFilePath(assetID, ".jpg")
}

func (c *Cache) backdropCachePath(assetID string) string {
	return c.cacheFilePath(assetID, ".backdrop.jpg")
}

func (c *Cache) cacheFilePath(assetID, suffix string) string {
	prefix := "00"
	if len(assetID) >= 2 {
		prefix = assetID[:2]
	}
	return filepath.Join(c.cacheDir, prefix, assetID+suffix)
}

// GetOrGenerate returns the preview JPEG for the asset (either in-memory bytes or disk file path).
func (c *Cache) GetOrGenerate(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(ctx, assetID, c.cachePath(assetID), func() ([]byte, error) {
		return c.generateBytes(ctx, sourcePath, orientation)
	})
}

// GetOrGenerateBackdrop returns a compact, pre-blurred, ~35%-darkened JPEG backdrop for the asset.
func (c *Cache) GetOrGenerateBackdrop(ctx context.Context, assetID, sourcePath string, orientation int) (Result, error) {
	return c.getOrCreate(ctx, backdropCacheKey(assetID), c.backdropCachePath(assetID), func() ([]byte, error) {
		return c.generateBackdropBytes(ctx, sourcePath, orientation)
	})
}

func (c *Cache) getOrCreate(ctx context.Context, cacheKey, targetPath string, generate func() ([]byte, error)) (Result, error) {
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

// Evict removes cached renditions (preview and backdrop) from memory and disk if present.
func (c *Cache) Evict(assetID string) {
	c.memLRU.Remove(assetID)
	c.memLRU.Remove(backdropCacheKey(assetID))
	_ = os.Remove(c.cachePath(assetID))
	_ = os.Remove(c.backdropCachePath(assetID))
}

func (c *Cache) decodeOrientedImage(ctx context.Context, sourcePath string, orientation int) (image.Image, error) {
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

func separableBoxBlurAndDim(src *image.RGBA, radius, passes int, dim float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || radius <= 0 || passes <= 0 {
		return src
	}

	cur := src
	tmp := image.NewRGBA(b)
	next := image.NewRGBA(b)
	win := 2*radius + 1

	for pass := 0; pass < passes; pass++ {
		// Horizontal 1D running-sum pass: cur -> tmp
		for y := 0; y < h; y++ {
			rowOff := y * cur.Stride
			var rSum, gSum, bSum int
			for dx := -radius; dx <= radius; dx++ {
				cx := min(w-1, max(0, dx))
				off := rowOff + cx*4
				rSum += int(cur.Pix[off])
				gSum += int(cur.Pix[off+1])
				bSum += int(cur.Pix[off+2])
			}
			for x := 0; x < w; x++ {
				dOff := y*tmp.Stride + x*4
				tmp.Pix[dOff] = uint8(rSum / win)
				tmp.Pix[dOff+1] = uint8(gSum / win)
				tmp.Pix[dOff+2] = uint8(bSum / win)
				tmp.Pix[dOff+3] = 255

				outX := max(0, x-radius)
				inX := min(w-1, x+radius+1)
				outOff := rowOff + outX*4
				inOff := rowOff + inX*4
				rSum += int(cur.Pix[inOff]) - int(cur.Pix[outOff])
				gSum += int(cur.Pix[inOff+1]) - int(cur.Pix[outOff+1])
				bSum += int(cur.Pix[inOff+2]) - int(cur.Pix[outOff+2])
			}
		}

		// Vertical 1D running-sum pass: tmp -> next
		for x := 0; x < w; x++ {
			colOff := x * 4
			var rSum, gSum, bSum int
			for dy := -radius; dy <= radius; dy++ {
				cy := min(h-1, max(0, dy))
				off := cy*tmp.Stride + colOff
				rSum += int(tmp.Pix[off])
				gSum += int(tmp.Pix[off+1])
				bSum += int(tmp.Pix[off+2])
			}
			for y := 0; y < h; y++ {
				dOff := y*next.Stride + colOff
				next.Pix[dOff] = uint8(rSum / win)
				next.Pix[dOff+1] = uint8(gSum / win)
				next.Pix[dOff+2] = uint8(bSum / win)
				next.Pix[dOff+3] = 255

				outY := max(0, y-radius)
				inY := min(h-1, y+radius+1)
				outOff := outY*tmp.Stride + colOff
				inOff := inY*tmp.Stride + colOff
				rSum += int(tmp.Pix[inOff]) - int(tmp.Pix[outOff])
				gSum += int(tmp.Pix[inOff+1]) - int(tmp.Pix[outOff+1])
				bSum += int(tmp.Pix[inOff+2]) - int(tmp.Pix[outOff+2])
			}
		}

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
	small = boxBlurAndDim(small, 3, 0.72)

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

func boxBlurAndDim(src *image.RGBA, radius int, dim float64) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	cur := src
	for pass := 0; pass < 2; pass++ {
		next := image.NewRGBA(b)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var rSum, gSum, bSum, cnt int
				for dy := -radius; dy <= radius; dy++ {
					ny := min(h-1, max(0, y+dy))
					for dx := -radius; dx <= radius; dx++ {
						nx := min(w-1, max(0, x+dx))
						off := ny*cur.Stride + nx*4
						rSum += int(cur.Pix[off])
						gSum += int(cur.Pix[off+1])
						bSum += int(cur.Pix[off+2])
						cnt++
					}
				}
				dOff := y*next.Stride + x*4
				next.Pix[dOff] = uint8(rSum / cnt)
				next.Pix[dOff+1] = uint8(gSum / cnt)
				next.Pix[dOff+2] = uint8(bSum / cnt)
				next.Pix[dOff+3] = 255
			}
		}
		cur = next
	}
	for i := 0; i < len(cur.Pix); i += 4 {
		cur.Pix[i] = uint8(float64(cur.Pix[i]) * dim)
		cur.Pix[i+1] = uint8(float64(cur.Pix[i+1]) * dim)
		cur.Pix[i+2] = uint8(float64(cur.Pix[i+2]) * dim)
	}
	return cur
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
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jpg") {
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
