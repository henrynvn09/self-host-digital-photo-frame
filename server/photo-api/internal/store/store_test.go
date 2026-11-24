package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"photo-api/internal/store"
)

func TestBackwardCompatibleVideoSchemaMigration(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "index.db")

	// Simulate pre-#19 schema without media_type or duration_sec columns.
	legacyDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	_, err = legacyDB.Exec(`
CREATE TABLE assets (
	id TEXT PRIMARY KEY,
	rel_path TEXT NOT NULL UNIQUE,
	mtime INTEGER NOT NULL,
	size INTEGER NOT NULL,
	taken_at TEXT NOT NULL,
	taken_year INTEGER NOT NULL,
	taken_month INTEGER NOT NULL,
	taken_day INTEGER NOT NULL,
	city TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	country TEXT NOT NULL DEFAULT '',
	album TEXT NOT NULL DEFAULT '',
	orientation INTEGER NOT NULL DEFAULT 1,
	width INTEGER NOT NULL DEFAULT 0,
	height INTEGER NOT NULL DEFAULT 0,
	indexed_at TEXT NOT NULL
);
INSERT INTO assets (
	id, rel_path, mtime, size, taken_at, taken_year, taken_month, taken_day,
	city, state, country, album, orientation, width, height, indexed_at
) VALUES (
	'legacy-photo-1', '2020/Trip/legacy.jpg', 1700000000, 4096,
	'2020-07-15T10:00:00Z', 2020, 7, 15,
	'Saigon', '', 'Vietnam', 'Trip', 1, 1600, 1200, '2024-01-01T00:00:00Z'
);`)
	if err != nil {
		_ = legacyDB.Close()
		t.Fatalf("seed legacy table: %v", err)
	}
	_ = legacyDB.Close()

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open migration failed: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	legacyAsset, err := st.GetAsset(ctx, "legacy-photo-1")
	if err != nil || legacyAsset == nil {
		t.Fatalf("get legacy asset after migration: asset=%v err=%v", legacyAsset, err)
	}
	if legacyAsset.MediaType != "IMAGE" || legacyAsset.DurationSec != 0 {
		t.Fatalf("expected migrated defaults MediaType=IMAGE DurationSec=0, got %+v", legacyAsset)
	}

	taken := time.Date(2023, 7, 15, 12, 0, 0, 0, time.UTC)
	err = st.UpsertAsset(ctx, store.Asset{
		ID:          "video-1",
		RelPath:     "2023/Trip/clip.mp4",
		MediaType:   "VIDEO",
		DurationSec: 14.5,
		Mtime:       taken.Unix(),
		Size:        204800,
		TakenAt:     taken,
		TakenYear:   2023,
		TakenMonth:  7,
		TakenDay:    15,
		Orientation: 1,
		Width:       1080,
		Height:      1920,
		IndexedAt:   taken,
	})
	if err != nil {
		t.Fatalf("upsert video asset: %v", err)
	}

	gotVideo, err := st.GetAsset(ctx, "video-1")
	if err != nil || gotVideo == nil {
		t.Fatalf("get video asset: asset=%v err=%v", gotVideo, err)
	}
	if gotVideo.MediaType != "VIDEO" || gotVideo.DurationSec != 14.5 {
		t.Fatalf("expected VIDEO 14.5s, got MediaType=%q DurationSec=%v", gotVideo.MediaType, gotVideo.DurationSec)
	}
}

func TestSearchRandomAtYear_IntraRoundDisjointAndReshuffle(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Insert 300 assets in Year 2018 (Oct - Nov)
	baseTime := time.Date(2018, 10, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-2018-oct-%03d", i),
			RelPath:     fmt.Sprintf("2018/Oct/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   taken.Year(),
			TakenMonth:  int(taken.Month()),
			TakenDay:    taken.Day(),
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// Assets outside the range (May 2018) to verify date-window isolation
	mayTime := time.Date(2018, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		taken := mayTime.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-2018-may-%03d", i),
			RelPath:     fmt.Sprintf("2018/May/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   taken.Year(),
			TakenMonth:  int(taken.Month()),
			TakenDay:    taken.Day(),
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert may: %v", err)
		}
	}

	startDate := time.Date(2018, 10, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2018, 11, 30, 23, 59, 59, 0, time.UTC)

	// Round 0: Days 1, 2, 3 (each draws 100 from 300 total)
	day1, err := st.SearchRandomAtYear(ctx, &startDate, &endDate, 100, 1, 2026)
	if err != nil || len(day1) != 100 {
		t.Fatalf("day 1 failed: len=%d, err=%v", len(day1), err)
	}

	// 1. Idempotency on same day
	day1Retry, err := st.SearchRandomAtYear(ctx, &startDate, &endDate, 100, 1, 2026)
	if err != nil {
		t.Fatal(err)
	}
	for i := range day1 {
		if day1[i].ID != day1Retry[i].ID {
			t.Fatalf("idempotency check failed at index %d", i)
		}
	}

	day2, err := st.SearchRandomAtYear(ctx, &startDate, &endDate, 100, 2, 2026)
	if err != nil || len(day2) != 100 {
		t.Fatalf("day 2 failed: len=%d, err=%v", len(day2), err)
	}

	day3, err := st.SearchRandomAtYear(ctx, &startDate, &endDate, 100, 3, 2026)
	if err != nil || len(day3) != 100 {
		t.Fatalf("day 3 failed: len=%d, err=%v", len(day3), err)
	}

	// Verify Days 1, 2, 3 are mutually disjoint and none are from May 2018
	seen := make(map[string]int)
	for _, a := range day1 {
		if a.TakenMonth == 5 {
			t.Fatalf("out-of-window May asset leaked into day 1: %s", a.ID)
		}
		seen[a.ID] = 1
	}
	for _, a := range day2 {
		if a.TakenMonth == 5 {
			t.Fatalf("out-of-window May asset leaked into day 2: %s", a.ID)
		}
		if seen[a.ID] != 0 {
			t.Fatalf("asset %s in day 2 was already seen in day %d", a.ID, seen[a.ID])
		}
		seen[a.ID] = 2
	}
	for _, a := range day3 {
		if a.TakenMonth == 5 {
			t.Fatalf("out-of-window May asset leaked into day 3: %s", a.ID)
		}
		if seen[a.ID] != 0 {
			t.Fatalf("asset %s in day 3 was already seen in day %d", a.ID, seen[a.ID])
		}
		seen[a.ID] = 3
	}

	if len(seen) != 300 {
		t.Fatalf("expected 300 unique assets covered across round 0, got %d", len(seen))
	}

	// 2. Round 1 begins on Day 4: freshly reshuffled!
	day4, err := st.SearchRandomAtYear(ctx, &startDate, &endDate, 100, 4, 2026)
	if err != nil || len(day4) != 100 {
		t.Fatalf("day 4 failed: len=%d, err=%v", len(day4), err)
	}

	// Day 4 (Round 1) should NOT be identical to Day 1 (Round 0)
	identicalCount := 0
	for i := 0; i < 100; i++ {
		if day4[i].ID == day1[i].ID {
			identicalCount++
		}
	}
	if identicalCount == 100 {
		t.Fatalf("Round 1 Day 4 was identical to Round 0 Day 1 (no reshuffle occurred!)")
	}

	// 3. Verify assets are readable
	for _, a := range day1 {
		dbAsset, err := st.GetAsset(ctx, a.ID)
		if err != nil {
			t.Fatalf("get asset error: %v", err)
		}
		if dbAsset.ID != a.ID {
			t.Fatalf("expected asset %s, got %s", a.ID, dbAsset.ID)
		}
	}
}

func TestSearchRandomAtYear_SeamlessStraddleStitch(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Insert 150 assets in Year 2020 (Aug)
	base2020 := time.Date(2020, 8, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 150; i++ {
		taken := base2020.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-wrap-%03d", i),
			RelPath:     fmt.Sprintf("2020/Aug/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2020,
			TakenMonth:  8,
			TakenDay:    1,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	startWrap := time.Date(2020, 8, 1, 0, 0, 0, 0, time.UTC)
	endWrap := time.Date(2020, 8, 31, 23, 59, 59, 0, time.UTC)

	// Day 1: globalStart = 0 -> Round 0, items [0..99]
	day1, err := st.SearchRandomAtYear(ctx, &startWrap, &endWrap, 100, 1, 2026)
	if err != nil || len(day1) != 100 {
		t.Fatalf("day 1 failed: len=%d, err=%v", len(day1), err)
	}

	// Day 2: globalStart = 100 -> Straddles Round 0 (50 items) + Round 1 (50 items)
	// Must return EXACTLY 100 items!
	day2, err := st.SearchRandomAtYear(ctx, &startWrap, &endWrap, 100, 2, 2026)
	if err != nil || len(day2) != 100 {
		t.Fatalf("day 2 straddle failed: len=%d, err=%v", len(day2), err)
	}

	// Verify all 100 items on day 2 are distinct (zero intra-day duplicates)
	day2Set := make(map[string]bool)
	for _, a := range day2 {
		if day2Set[a.ID] {
			t.Fatalf("duplicate asset %s found within day 2 straddle batch", a.ID)
		}
		day2Set[a.ID] = true
	}

	// The first 50 items of Day 2 are the remainder of Round 0, so they must be disjoint from Day 1
	day1Set := make(map[string]bool)
	for _, a := range day1 {
		day1Set[a.ID] = true
	}
	for i := 0; i < 50; i++ {
		if day1Set[day2[i].ID] {
			t.Fatalf("item %s in first piece of day 2 was already in day 1", day2[i].ID)
		}
	}

	// Day 3: globalStart = 200 -> Round 1, items [50..149]
	// Must return EXACTLY 100 items!
	day3, err := st.SearchRandomAtYear(ctx, &startWrap, &endWrap, 100, 3, 2026)
	if err != nil || len(day3) != 100 {
		t.Fatalf("day 3 failed: len=%d, err=%v", len(day3), err)
	}

	// Day 3 should not overlap with the second half of Day 2 (which were items [0..49] of Round 1)
	day2SecondHalf := make(map[string]bool)
	for i := 50; i < 100; i++ {
		day2SecondHalf[day2[i].ID] = true
	}
	for _, a := range day3 {
		if day2SecondHalf[a.ID] {
			t.Fatalf("item %s in day 3 overlapped with second piece of day 2", a.ID)
		}
	}
}

// TestSearchRandomAtYear_MultiRoundStraddleChain verifies intra-day uniqueness across multi-round straddles (round >= 2)
// where K < N < 2K (e.g. N=105, K=100) across 10 consecutive days.
func TestSearchRandomAtYear_MultiRoundStraddleChain(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// 105 assets, K = 100: straddles occur on Day 2, Day 3, Day 4, Day 5...
	baseTime := time.Date(2022, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 105; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("chain-%03d", i),
			RelPath:     fmt.Sprintf("2022/Jan/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2022,
			TakenMonth:  1,
			TakenDay:    1,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	start := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2022, 1, 31, 23, 59, 59, 0, time.UTC)

	for day := 1; day <= 10; day++ {
		res, err := st.SearchRandomAtYear(ctx, &start, &end, 100, day, 2026)
		if err != nil {
			t.Fatalf("day %d failed: %v", day, err)
		}
		if len(res) != 100 {
			t.Fatalf("day %d: expected 100 items, got %d", day, len(res))
		}

		// Verify 100% intra-day uniqueness (zero duplicates within any day's batch)
		seen := make(map[string]bool, 100)
		for _, a := range res {
			if seen[a.ID] {
				t.Fatalf("day %d (straddle round >= 1) contained duplicate asset %s", day, a.ID)
			}
			seen[a.ID] = true
		}
	}
}

func TestSearchRandomAtYear_UnderFillDailyVariation(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// 35 assets in window, requested size=100
	baseTime := time.Date(2019, 6, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 35; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-underfill-%02d", i),
			RelPath:     fmt.Sprintf("2019/June/photo_%02d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2019,
			TakenMonth:  6,
			TakenDay:    1,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	startUnderfill := time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)
	endUnderfill := time.Date(2019, 6, 30, 23, 59, 59, 0, time.UTC)

	// Day 1
	resDay1, err := st.SearchRandomAtYear(ctx, &startUnderfill, &endUnderfill, 100, 1, 2026)
	if err != nil || len(resDay1) != 35 {
		t.Fatalf("day 1 underfill failed: len=%d, err=%v", len(resDay1), err)
	}

	// Day 2
	resDay2, err := st.SearchRandomAtYear(ctx, &startUnderfill, &endUnderfill, 100, 2, 2026)
	if err != nil || len(resDay2) != 35 {
		t.Fatalf("day 2 underfill failed: len=%d, err=%v", len(resDay2), err)
	}

	// Orders between Day 1 and Day 2 must be different (dynamic daily shuffle)
	identicalIndices := 0
	for i := range resDay1 {
		if resDay1[i].ID == resDay2[i].ID {
			identicalIndices++
		}
	}
	if identicalIndices == 35 {
		t.Fatalf("underfill order was static across days! Expected dynamic permutation.")
	}

	// Test leap day (dayOfYear = 366)
	resLeap, err := st.SearchRandomAt(ctx, &startUnderfill, &endUnderfill, 100, 366)
	if err != nil || len(resLeap) != 35 {
		t.Fatalf("leap day failed: len=%d, err=%v", len(resLeap), err)
	}
}

func TestSearchRandomAtYear_MultiYearSeedVariation(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	baseTime := time.Date(2015, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-my-%02d", i),
			RelPath:     fmt.Sprintf("2015/May/photo_%02d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2015,
			TakenMonth:  5,
			TakenDay:    1,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	start := time.Date(2015, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2015, 5, 31, 23, 59, 59, 0, time.UTC)

	// Day 1 in 2026 vs Day 1 in 2027
	res2026, err := st.SearchRandomAtYear(ctx, &start, &end, 30, 1, 2026)
	if err != nil || len(res2026) != 30 {
		t.Fatalf("2026 failed: len=%d, err=%v", len(res2026), err)
	}

	res2027, err := st.SearchRandomAtYear(ctx, &start, &end, 30, 1, 2027)
	if err != nil || len(res2027) != 30 {
		t.Fatalf("2027 failed: len=%d, err=%v", len(res2027), err)
	}

	matches := 0
	for i := range res2026 {
		if res2026[i].ID == res2027[i].ID {
			matches++
		}
	}
	if matches == 30 {
		t.Fatalf("expected different permutation between year 2026 and 2027")
	}
}

func TestSearchRandomStatelessWithoutDateRange(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 10; i++ {
		err := st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("stateless-%d", i),
			RelPath:     fmt.Sprintf("photo_%d.jpg", i),
			Mtime:       now.Unix(),
			Size:        1000,
			TakenAt:     now,
			TakenYear:   now.Year(),
			TakenMonth:  int(now.Month()),
			TakenDay:    now.Day(),
			Orientation: 1,
			IndexedAt:   now,
		})
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	res, err := st.SearchRandom(ctx, nil, nil, 5)
	if err != nil || len(res) != 5 {
		t.Fatalf("stateless random failed: len %d, err %v", len(res), err)
	}

	for _, a := range res {
		dbAsset, err := st.GetAsset(ctx, a.ID)
		if err != nil {
			t.Fatalf("get asset error: %v", err)
		}
		if dbAsset.ID != a.ID {
			t.Fatalf("expected asset %s, got %s", a.ID, dbAsset.ID)
		}
	}
}
