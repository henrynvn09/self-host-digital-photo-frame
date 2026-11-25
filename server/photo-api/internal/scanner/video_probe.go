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

func parsePositiveFloat(raw string) (float64, bool) {
	d, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return 0, false
	}
	return d, true
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
	if d, ok := parsePositiveFloat(payload.Format.Duration); ok {
		meta.durationSec = d
	}

	for _, st := range payload.Streams {
		if st.CodecType != "video" {
			continue
		}
		w, h := st.Width, st.Height
		if meta.durationSec <= 0 {
			if d, ok := parsePositiveFloat(st.Duration); ok {
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

func firstTagMatch[T any](payload ffprobePayload, keys []string, parse func(string) (T, bool)) (T, bool) {
	tagMaps := tagMapsInPriority(payload)
	for _, key := range keys {
		for _, tags := range tagMaps {
			if raw, ok := lookupTagValue(tags, key); ok {
				if parsed, ok := parse(raw); ok {
					return parsed, true
				}
			}
		}
	}
	var zero T
	return zero, false
}

func extractVideoCaptureDate(payload ffprobePayload) time.Time {
	t, _ := firstTagMatch(payload, []string{"com.apple.quicktime.creationdate", "creation_time"}, parseVideoTimestamp)
	return t
}

func extractVideoGPS(payload ffprobePayload) (float64, float64, bool) {
	type coords struct{ lat, lon float64 }
	c, ok := firstTagMatch(payload, []string{"com.apple.quicktime.location.ISO6709", "location"}, func(raw string) (coords, bool) {
		lat, lon, ok := parseISO6709(raw)
		return coords{lat: lat, lon: lon}, ok
	})
	return c.lat, c.lon, ok
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

func buildVideoAssetFromMeta(meta videoProbeMeta, c scanCandidate, loadGeo func() *geo.Resolver) store.Asset {
	a := newBaseAsset(c, "VIDEO")
	a.DurationSec = meta.durationSec
	a.Width = meta.width
	a.Height = meta.height

	if meta.hasGPS {
		applyGeoLocation(&a, meta.lat, meta.lon, loadGeo)
	}
	applyTakenDate(&a, meta.captureDate, c.relSlash, c.modTime)
	return a
}
