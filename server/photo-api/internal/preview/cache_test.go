package preview_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"photo-api/internal/preview"
)

func TestMemoryLRUEviction(t *testing.T) {
	// Max capacity = 1,000 bytes
	lru := preview.NewMemoryLRU(1000)

	// Insert item1 (400 bytes)
	data1 := make([]byte, 400)
	lru.Put("img1", data1)

	count, bytesUsed := lru.Stats()
	if count != 1 || bytesUsed != 400 {
		t.Fatalf("expected 1 item and 400 bytes, got %d items and %d bytes", count, bytesUsed)
	}

	// Insert item2 (400 bytes)
	data2 := make([]byte, 400)
	lru.Put("img2", data2)

	count, bytesUsed = lru.Stats()
	if count != 2 || bytesUsed != 800 {
		t.Fatalf("expected 2 items and 800 bytes, got %d items and %d bytes", count, bytesUsed)
	}

	// Access img1 so it becomes most recently used
	if _, ok := lru.Get("img1"); !ok {
		t.Fatalf("expected img1 to be found")
	}

	// Insert item3 (400 bytes). Total would be 1200 > 1000, so least recently used (img2) must be evicted!
	data3 := make([]byte, 400)
	lru.Put("img3", data3)

	count, bytesUsed = lru.Stats()
	if count != 2 || bytesUsed != 800 {
		t.Fatalf("expected 2 items and 800 bytes after eviction, got %d items and %d bytes", count, bytesUsed)
	}

	if _, ok := lru.Get("img2"); ok {
		t.Fatalf("expected img2 to have been evicted")
	}
	if _, ok := lru.Get("img1"); !ok {
		t.Fatalf("expected img1 to still be present (it was accessed recently)")
	}
	if _, ok := lru.Get("img3"); !ok {
		t.Fatalf("expected img3 to be present")
	}
}

func TestMemoryLRUConcurrent(t *testing.T) {
	lru := preview.NewMemoryLRU(10000)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("asset-%d", id%10)
			data := make([]byte, 200)
			lru.Put(key, data)
			_, _ = lru.Get(key)
			if id%5 == 0 {
				lru.Remove(key)
			}
		}(i)
	}
	wg.Wait()

	_, bytesUsed := lru.Stats()
	if bytesUsed > 10000 {
		t.Fatalf("bytesUsed %d exceeds maxBytes 10000", bytesUsed)
	}
}

func createTestJPEG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}
	path := filepath.Join(dir, name)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write test jpeg: %v", err)
	}
	return path
}

func TestCacheMemoryMode(t *testing.T) {
	dataDir := t.TempDir()
	photosDir := t.TempDir()

	testImgPath := createTestJPEG(t, photosDir, "photo1.jpg", 800, 600)

	cache, err := preview.NewCache(dataDir, preview.Options{
		Mode:                 "memory",
		MaxWidth:             400,
		MaxHeight:            300,
		JPEGQuality:          80,
		MaxMB:                50,
		MaxConcurrentDecodes: 2,
	})
	if err != nil {
		t.Fatalf("NewCache failed: %v", err)
	}

	ctx := context.Background()
	res, err := cache.GetOrGenerate(ctx, "asset-001", testImgPath, 1)
	if err != nil {
		t.Fatalf("GetOrGenerate failed: %v", err)
	}

	if len(res.Data) == 0 {
		t.Fatalf("expected non-empty Data in memory mode")
	}
	if res.Path != "" {
		t.Fatalf("expected empty Path in memory mode, got %s", res.Path)
	}

	// Disk cache directory should not have been created or written to
	cacheFiles, _ := os.ReadDir(filepath.Join(dataDir, "cache"))
	if len(cacheFiles) > 0 {
		t.Fatalf("expected 0 files on disk in memory mode, found %d", len(cacheFiles))
	}

	// Second request should hit memory cache immediately
	res2, err := cache.GetOrGenerate(ctx, "asset-001", testImgPath, 1)
	if err != nil {
		t.Fatalf("second GetOrGenerate failed: %v", err)
	}
	if len(res2.Data) != len(res.Data) {
		t.Fatalf("expected cached data length to match")
	}

	// Evict should remove from memory
	cache.Evict("asset-001")
}
