package preview

import (
	"bytes"
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

type Cache struct {
	cacheDir    string
	maxWidth    int
	maxHeight   int
	jpegQuality int
	maxBytes    int64

	sf      singleflight.Group
	evictMu sync.Mutex
}

func NewCache(dataDir string, maxWidth, maxHeight, jpegQuality int, maxMB int64) (*Cache, error) {
	cacheDir := filepath.Join(dataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create preview cache dir %s: %w", cacheDir, err)
	}
	if maxWidth <= 0 {
		maxWidth = 2560
	}
	if maxHeight <= 0 {
		maxHeight = 1440
	}
	if jpegQuality <= 0 || jpegQuality > 100 {
		jpegQuality = 88
	}
	if maxMB <= 0 {
		maxMB = 1024
	}
	return &Cache{
		cacheDir:    cacheDir,
		maxWidth:    maxWidth,
		maxHeight:   maxHeight,
		jpegQuality: jpegQuality,
		maxBytes:    maxMB * 1024 * 1024,
	}, nil
}

func (c *Cache) cachePath(assetID string) string {
	prefix := "00"
	if len(assetID) >= 2 {
		prefix = assetID[:2]
	}
	return filepath.Join(c.cacheDir, prefix, assetID+".jpg")
}

// GetOrGenerate returns the path to a cached 2K JPEG Preview for the asset,
// generating and caching it lazily on first request with singleflight coalescing.
func (c *Cache) GetOrGenerate(ctx context.Context, assetID, sourcePath string, orientation int) (string, error) {
	target := c.cachePath(assetID)
	if info, err := os.Stat(target); err == nil && info.Size() > 0 {
		now := time.Now()
		_ = os.Chtimes(target, now, now)
		return target, nil
	}

	val, err, _ := c.sf.Do(assetID, func() (any, error) {
		if info, err := os.Stat(target); err == nil && info.Size() > 0 {
			now := time.Now()
			_ = os.Chtimes(target, now, now)
			return target, nil
		}
		if err := c.generate(ctx, sourcePath, target, orientation); err != nil {
			return "", err
		}
		c.enforceLimit(target)
		return target, nil
	})
	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// Evict removes a cached preview if present.
func (c *Cache) Evict(assetID string) {
	_ = os.Remove(c.cachePath(assetID))
}

func (c *Cache) generate(ctx context.Context, sourcePath, targetPath string, orientation int) error {
	img, err := decodeImage(ctx, sourcePath)
	if err != nil {
		return err
	}

	if orientation > 1 && orientation <= 8 {
		img = applyOrientation(img, orientation)
	}

	img = fitWithin(img, c.maxWidth, c.maxHeight)

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: c.jpegQuality}); err != nil {
		return fmt.Errorf("encode preview jpeg: %w", err)
	}

	tmpPath := targetPath + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o644); err != nil {
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

// applyOrientation transforms an image according to EXIF orientation tags 2-8.
func applyOrientation(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	switch orientation {
	case 2: // Flip horizontal
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 3: // Rotate 180
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 4: // Flip vertical
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 5: // Transpose (flip horizontal + rotate 270 CW)
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 6: // Rotate 90 CW
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 7: // Transverse (flip horizontal + rotate 90 CW)
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 8: // Rotate 270 CW
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	default:
		return src
	}
}

type cachedFile struct {
	path  string
	size  int64
	mtime time.Time
}

func (c *Cache) enforceLimit(keepPath string) {
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
