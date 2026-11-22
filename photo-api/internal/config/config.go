package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port            string
	PhotosDir       string
	DataDir         string
	GeoNamesDir     string
	APIKey          string
	ScanInterval    time.Duration
	ExcludePatterns []string
	MaxWidth        int
	MaxHeight       int
	JPEGQuality     int
	CacheMaxMB      int64
}

func Load() Config {
	return Config{
		Port:            getEnv("PORT", "2283"),
		PhotosDir:       getEnv("PHOTOS_DIR", "/photos"),
		DataDir:         getEnv("DATA_DIR", "/data"),
		GeoNamesDir:     getEnv("GEONAMES_DIR", "/app/geonames"),
		APIKey:          os.Getenv("API_KEY"),
		ScanInterval:    getEnvDuration("SCAN_INTERVAL", 30*time.Minute),
		ExcludePatterns: getEnvList("EXCLUDE_PATTERNS"),
		MaxWidth:        getEnvInt("MAX_WIDTH", 2560),
		MaxHeight:       getEnvInt("MAX_HEIGHT", 1440),
		JPEGQuality:     getEnvInt("JPEG_QUALITY", 88),
		CacheMaxMB:      int64(getEnvInt("CACHE_MAX_MB", 1024)),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func getEnvList(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
