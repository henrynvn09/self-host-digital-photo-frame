package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"photo-api/internal/api"
	"photo-api/internal/config"
	"photo-api/internal/preview"
	"photo-api/internal/scanner"
	"photo-api/internal/store"
)

func checkDirectories(libraryDir, dataDir string) error {
	entries, err := os.ReadDir(libraryDir)
	if err != nil {
		return fmt.Errorf("cannot read library %q (check Synology PUID/PGID and volume mount): %w", libraryDir, err)
	}
	slog.Info("library readable", "path", libraryDir, "top_level_entries", len(entries))

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("cannot create DATA_DIR %q (check Synology PUID/PGID permissions): %w", dataDir, err)
	}
	probe := filepath.Join(dataDir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("DATA_DIR %q is not writable (check Synology PUID/PGID permissions): %w", dataDir, err)
	}
	_ = os.Remove(probe)
	slog.Info("data directory writable", "path", dataDir)
	return nil
}

func main() {
	cfg := config.Load()
	slog.Info("starting photo-api",
		"port", cfg.Port,
		"library_dir", cfg.LibraryDir,
		"data_dir", cfg.DataDir,
		"max_resolution", fmt.Sprintf("%dx%d", cfg.MaxWidth, cfg.MaxHeight),
		"prebake_blur", cfg.PrebakeBlur,
		"frame_resolution", fmt.Sprintf("%dx%d", cfg.FrameWidth, cfg.FrameHeight),
		"cache_mode", cfg.CacheMode,
		"cache_max_mb", cfg.CacheMaxMB,
		"max_concurrent_decodes", cfg.MaxConcurrentDecodes,
		"video_max_resolution", fmt.Sprintf("%dx%d", cfg.VideoMaxWidth, cfg.VideoMaxHeight),
		"video_fps", cfg.VideoFPS,
		"max_concurrent_video_transcodes", cfg.MaxConcurrentVideoTranscode,
		"min_video_seconds", cfg.MinVideoSeconds,
		"live_photo_max_seconds", cfg.LivePhotoMaxSeconds,
		"scan_interval", cfg.ScanInterval.String(),
		"auth_enabled", cfg.APIKey != "",
		"uid", os.Getuid(),
		"gid", os.Getgid(),
	)

	if err := checkDirectories(cfg.LibraryDir, cfg.DataDir); err != nil {
		slog.Error("startup check failed", "error", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		slog.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	cache, err := preview.NewCache(cfg.DataDir, preview.Options{
		Mode:                        cfg.CacheMode,
		MaxWidth:                    cfg.MaxWidth,
		MaxHeight:                   cfg.MaxHeight,
		JPEGQuality:                 cfg.JPEGQuality,
		MaxMB:                       cfg.CacheMaxMB,
		PrebakeBlur:                 cfg.PrebakeBlur,
		FrameWidth:                  cfg.FrameWidth,
		FrameHeight:                 cfg.FrameHeight,
		MaxConcurrentDecodes:        cfg.MaxConcurrentDecodes,
		VideoMaxWidth:               cfg.VideoMaxWidth,
		VideoMaxHeight:              cfg.VideoMaxHeight,
		VideoFPS:                    cfg.VideoFPS,
		MaxConcurrentVideoTranscode: cfg.MaxConcurrentVideoTranscode,
	})
	if err != nil {
		slog.Error("init preview cache failed", "error", err)
		os.Exit(1)
	}

	sc := scanner.New(cfg.LibraryDir, cfg.GeoNamesDir, cfg.ExcludePatterns, st, cache, scanner.Options{
		MinVideoSeconds:     cfg.MinVideoSeconds,
		LivePhotoMaxSeconds: cfg.LivePhotoMaxSeconds,
	})

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// Run initial scan in background so /api/server/ping is healthy immediately
	go func() {
		if _, err := sc.Scan(rootCtx); err != nil {
			slog.Error("initial scan failed", "error", err)
		}
		ticker := time.NewTicker(cfg.ScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				if _, err := sc.Scan(rootCtx); err != nil {
					slog.Error("periodic scan failed", "error", err)
				}
			}
		}
	}()

	apiServer := api.NewServer(cfg.LibraryDir, cfg.APIKey, st, sc, cache)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	rootCancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
