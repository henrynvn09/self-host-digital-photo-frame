package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"photo-api/internal/geo"
	"photo-api/internal/store"
)

var reISO6709 = regexp.MustCompile(`^([+-]\d+(?:\.\d+)?)([+-]\d+(?:\.\d+)?)(?:[+-]\d+(?:\.\d+)?)?$`)

var videoTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05.999999999 -0700",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05-0700",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006:01:02 15:04:05",
}

type ffprobeSideData struct {
	SideDataType string   `json:"side_data_type"`
	Rotation     *float64 `json:"rotation"`
}

type ffprobeStream struct {
	CodecType    string            `json:"codec_type"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Duration     string            `json:"duration"`
	Tags         map[string]string `json:"tags"`
	SideDataList []ffprobeSideData `json:"side_data_list"`
}

type ffprobeFormat struct {
	Duration string            `json:"duration"`
	Tags     map[string]string `json:"tags"`
}

type ffprobePayload struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type videoProbeMeta struct {
	durationSec float64
	width       int
	height      int
	captureDate time.Time
	lat         float64
	lon         float64
	hasGPS      bool
}

func probeVideo(ctx context.Context, fullPath string) (videoProbeMeta, error) {
	probeCtx, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		probeCtx,
		"ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		fullPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return videoProbeMeta{}, fmt.Errorf("ffprobe %s: %w", fullPath, err)
	}

	var payload ffprobePayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return videoProbeMeta{}, fmt.Errorf("decode ffprobe json: %w", err)
	}

	var meta videoProbeMeta
	if d, err := strconv.ParseFloat(strings.TrimSpace(payload.Format.Duration), 64); err == nil && d > 0 && !math.IsNaN(d) && !math.IsInf(d, 0) {
		meta.durationSec = d
	}

	for _, st := range payload.Streams {
		if st.CodecType != "video" {
			continue
		}
		w, h := st.Width, st.Height
		if meta.durationSec <= 0 {
			if d, err := strconv.ParseFloat(strings.TrimSpace(st.Duration), 64); err == nil && d > 0 && !math.IsNaN(d) && !math.IsInf(d, 0) {
				meta.durationSec = d
			}
		}
		rot := extractStreamRotation(st)
		if rot == 90 || rot == 270 {
			w, h = h, w
		}
		meta.width = w
		meta.height = h
		break
	}

	meta.captureDate = extractVideoCaptureDate(payload)
	meta.lat, meta.lon, meta.hasGPS = extractVideoGPS(payload)
	return meta, nil
}

func tagMapsInPriority(payload ffprobePayload) []map[string]string {
	maps := make([]map[string]string, 0, 1+len(payload.Streams))
	if len(payload.Format.Tags) > 0 {
		maps = append(maps, payload.Format.Tags)
	}
	for _, st := range payload.Streams {
		if st.CodecType == "video" && len(st.Tags) > 0 {
			maps = append(maps, st.Tags)
		}
	}
	for _, st := range payload.Streams {
		if st.CodecType != "video" && len(st.Tags) > 0 {
			maps = append(maps, st.Tags)
		}
	}
	return maps
}

func lookupTagValue(tags map[string]string, target string) (string, bool) {
	if len(tags) == 0 {
		return "", false
	}
	for k, v := range tags {
		if strings.EqualFold(k, target) {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed, true
			}
		}
	}
	prefix := strings.ToLower(target) + "-"
	var matchedKey, matchedVal string
	for k, v := range tags {
		if strings.HasPrefix(strings.ToLower(k), prefix) {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				if matchedKey == "" || k < matchedKey {
					matchedKey = k
					matchedVal = trimmed
				}
			}
		}
	}
	if matchedKey != "" {
		return matchedVal, true
	}
	return "", false
}

func parseVideoTimestamp(raw string) (time.Time, bool) {
	s := strings.TrimSpace(strings.TrimRight(raw, "\x00"))
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range videoTimeLayouts {
		t, err := time.Parse(layout, s)
		if err != nil || t.IsZero() {
			continue
		}
		if t.Year() < 1970 || t.UTC().Equal(time.Unix(0, 0).UTC()) {
			return time.Time{}, false
		}
		// Preserve the camera's local wall-clock date/time (matching EXIF DateTimeOriginal)
		// so evening/morning timezone offsets sort into the historical calendar date on the frame.
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), true
	}
	return time.Time{}, false
}

func extractVideoCaptureDate(payload ffprobePayload) time.Time {
	tagMaps := tagMapsInPriority(payload)
	for _, key := range []string{"com.apple.quicktime.creationdate", "creation_time"} {
		for _, tags := range tagMaps {
			if raw, ok := lookupTagValue(tags, key); ok {
				if t, ok := parseVideoTimestamp(raw); ok {
					return t
				}
			}
		}
	}
	return time.Time{}
}

func extractVideoGPS(payload ffprobePayload) (float64, float64, bool) {
	tagMaps := tagMapsInPriority(payload)
	for _, key := range []string{"com.apple.quicktime.location.ISO6709", "location"} {
		for _, tags := range tagMaps {
			if raw, ok := lookupTagValue(tags, key); ok {
				if lat, lon, ok := parseISO6709(raw); ok {
					return lat, lon, true
				}
			}
		}
	}
	return 0, 0, false
}

func parseISO6709(raw string) (float64, float64, bool) {
	s := strings.TrimSpace(strings.TrimRight(raw, "\x00"))
	s = strings.TrimSuffix(s, "/")
	if idx := strings.Index(strings.ToUpper(s), "CRS"); idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	m := reISO6709.FindStringSubmatch(s)
	if len(m) < 3 {
		return 0, 0, false
	}
	lat, ok1 := parseISO6709Component(m[1], false)
	lon, ok2 := parseISO6709Component(m[2], true)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
		return 0, 0, false
	}
	return lat, lon, true
}

func parseISO6709Component(token string, isLon bool) (float64, bool) {
	if len(token) < 2 {
		return 0, false
	}
	sign := 1.0
	switch token[0] {
	case '-':
		sign = -1.0
	case '+':
	default:
		return 0, false
	}
	body := token[1:]
	intPart := body
	if dot := strings.IndexByte(body, '.'); dot >= 0 {
		intPart = body[:dot]
	}

	degDigits := 2
	if isLon {
		degDigits = 3
	}

	switch {
	case len(intPart) >= 1 && len(intPart) <= degDigits:
		deg, err := strconv.ParseFloat(body, 64)
		if err != nil || math.IsNaN(deg) || math.IsInf(deg, 0) {
			return 0, false
		}
		return sign * deg, true
	case len(intPart) == degDigits+2:
		deg, err1 := strconv.ParseFloat(body[:degDigits], 64)
		mins, err2 := strconv.ParseFloat(body[degDigits:], 64)
		if err1 != nil || err2 != nil || mins < 0 || mins >= 60 {
			return 0, false
		}
		return sign * (deg + mins/60.0), true
	case len(intPart) == degDigits+4:
		deg, err1 := strconv.ParseFloat(body[:degDigits], 64)
		mins, err2 := strconv.ParseFloat(body[degDigits:degDigits+2], 64)
		secs, err3 := strconv.ParseFloat(body[degDigits+2:], 64)
		if err1 != nil || err2 != nil || err3 != nil || mins < 0 || mins >= 60 || secs < 0 || secs >= 60 {
			return 0, false
		}
		return sign * (deg + mins/60.0 + secs/3600.0), true
	default:
		return 0, false
	}
}

func extractStreamRotation(st ffprobeStream) int {
	for _, sd := range st.SideDataList {
		if sd.Rotation != nil {
			deg := ((int(math.Round(*sd.Rotation)) % 360) + 360) % 360
			return deg
		}
	}
	for k, v := range st.Tags {
		if strings.EqualFold(k, "rotate") {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return ((int(math.Round(f)) % 360) + 360) % 360
			}
		}
	}
	return 0
}

func buildVideoAssetFromMeta(meta videoProbeMeta, relSlash string, mtime, size int64, modTime time.Time, loadGeo func() *geo.Resolver) store.Asset {
	a := store.Asset{
		ID:          DeterministicUUID(relSlash),
		RelPath:     relSlash,
		MediaType:   "VIDEO",
		DurationSec: meta.durationSec,
		Mtime:       mtime,
		Size:        size,
		Album:       deriveAlbum(relSlash),
		Orientation: 1,
		Width:       meta.width,
		Height:      meta.height,
		IndexedAt:   time.Now().UTC(),
	}

	if meta.hasGPS && loadGeo != nil {
		if r := loadGeo(); r != nil {
			if loc, ok := r.Lookup(meta.lat, meta.lon); ok {
				a.City = loc.City
				a.State = loc.State
				a.Country = loc.Country
			}
		}
	}

	a.TakenAt = resolveDate(meta.captureDate, relSlash, modTime)
	a.TakenYear = a.TakenAt.UTC().Year()
	a.TakenMonth = int(a.TakenAt.UTC().Month())
	a.TakenDay = a.TakenAt.UTC().Day()
	return a
}
