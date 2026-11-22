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

func checkDirectories(photosDir, dataDir string) error {
	entries, err := os.ReadDir(photosDir)
	if err != nil {
		return fmt.Errorf("cannot read PHOTOS_DIR %q (check Synology PUID/PGID and volume mount): %w", photosDir, err)
	}
	slog.Info("library readable", "path", photosDir, "top_level_entries", len(entries))

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
		"photos_dir", cfg.PhotosDir,
		"data_dir", cfg.DataDir,
		"max_resolution", fmt.Sprintf("%dx%d", cfg.MaxWidth, cfg.MaxHeight),
		"cache_max_mb", cfg.CacheMaxMB,
		"scan_interval", cfg.ScanInterval.String(),
		"auth_enabled", cfg.APIKey != "",
		"uid", os.Getuid(),
		"gid", os.Getgid(),
	)

	if err := checkDirectories(cfg.PhotosDir, cfg.DataDir); err != nil {
		slog.Error("startup check failed", "error", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		slog.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	cache, err := preview.NewCache(cfg.DataDir, cfg.MaxWidth, cfg.MaxHeight, cfg.JPEGQuality, cfg.CacheMaxMB)
	if err != nil {
		slog.Error("init preview cache failed", "error", err)
		os.Exit(1)
	}

	sc := scanner.New(cfg.PhotosDir, cfg.GeoNamesDir, cfg.ExcludePatterns, st, cache)

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

	apiServer := api.NewServer(cfg.PhotosDir, cfg.APIKey, st, sc, cache)
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
