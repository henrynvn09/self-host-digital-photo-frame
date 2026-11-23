package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"photo-api/internal/store"
)

func TestSearchRandomAt_DeterministicAndDisjoint(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Insert 250 assets in Year 2018 (Oct - Nov), and 50 assets in Year 2018 outside range (May)
	baseTime := time.Date(2018, 10, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
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

	// Assets outside the range (May 2018)
	mayTime := time.Date(2018, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		taken := mayTime.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
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
	}

	// Query with 90-day anniversary window around Oct 15
	startDate := time.Date(2018, 10, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2018, 11, 30, 23, 59, 59, 0, time.UTC)

	// 1. Day 1 (dayOfYear = 100): size=100
	resultsDay1_a, err := st.SearchRandomAt(ctx, &startDate, &endDate, 100, 100)
	if err != nil {
		t.Fatalf("SearchRandomAt day 100 failed: %v", err)
	}
	if len(resultsDay1_a) != 100 {
		t.Fatalf("expected 100 items on day 100, got %d", len(resultsDay1_a))
	}

	// 2. Idempotency on same day: calling again on day 100 returns exact same items in exact same order
	resultsDay1_b, err := st.SearchRandomAt(ctx, &startDate, &endDate, 100, 100)
	if err != nil {
		t.Fatalf("SearchRandomAt day 100 retry failed: %v", err)
	}
	for i := range resultsDay1_a {
		if resultsDay1_a[i].ID != resultsDay1_b[i].ID {
			t.Fatalf("idempotency failed at index %d: %s vs %s", i, resultsDay1_a[i].ID, resultsDay1_b[i].ID)
		}
	}

	// 3. Day 2 (dayOfYear = 101): size=100. Must return 100 items completely disjoint from Day 1!
	resultsDay2, err := st.SearchRandomAt(ctx, &startDate, &endDate, 100, 101)
	if err != nil {
		t.Fatalf("SearchRandomAt day 101 failed: %v", err)
	}
	if len(resultsDay2) != 100 {
		t.Fatalf("expected 100 items on day 101, got %d", len(resultsDay2))
	}

	seenInDay1 := make(map[string]bool)
	for _, a := range resultsDay1_a {
		seenInDay1[a.ID] = true
	}
	for _, a := range resultsDay2 {
		if seenInDay1[a.ID] {
			t.Fatalf("asset %s was returned on both day 100 and day 101 (replacement occurred!)", a.ID)
		}
	}

	// 4. Zero DB writes: dealt_at must remain nil
	for _, a := range resultsDay1_a {
		dbAsset, err := st.GetAsset(ctx, a.ID)
		if err != nil {
			t.Fatalf("get asset: %v", err)
		}
		if dbAsset.DealtAt != nil {
			t.Fatalf("expected dealt_at to remain nil, got %v", dbAsset.DealtAt)
		}
	}
}

func TestSearchRandomAt_UnderFillAndWraparound(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// 1. Under-fill test: 35 assets in window, requested size=100
	baseTime := time.Date(2019, 6, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 35; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
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
	}

	startUnderfill := time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC)
	endUnderfill := time.Date(2019, 6, 30, 23, 59, 59, 0, time.UTC)

	resUnderfill, err := st.SearchRandomAt(ctx, &startUnderfill, &endUnderfill, 100, 1)
	if err != nil {
		t.Fatalf("underfill query failed: %v", err)
	}
	if len(resUnderfill) != 35 {
		t.Fatalf("expected exactly 35 items under-fill, got %d", len(resUnderfill))
	}

	// 2. Wraparound test: 150 assets in window, size=100
	// Day 1 (dayOfYear = 1): offset = (0 * 100) % 150 = 0 -> items [0..99]
	// Day 2 (dayOfYear = 2): offset = (1 * 100) % 150 = 100 -> items [100..149] + [0..49] (wraparound)
	base2020 := time.Date(2020, 8, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 150; i++ {
		taken := base2020.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
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
	}

	startWrap := time.Date(2020, 8, 1, 0, 0, 0, 0, time.UTC)
	endWrap := time.Date(2020, 8, 31, 23, 59, 59, 0, time.UTC)

	resDay1, err := st.SearchRandomAt(ctx, &startWrap, &endWrap, 100, 1)
	if err != nil || len(resDay1) != 100 {
		t.Fatalf("day 1 wrap query failed: len %d, err %v", len(resDay1), err)
	}

	resDay2, err := st.SearchRandomAt(ctx, &startWrap, &endWrap, 100, 2)
	if err != nil || len(resDay2) != 100 {
		t.Fatalf("day 2 wrap query failed: len %d, err %v", len(resDay2), err)
	}

	// Verify all 100 returned items on day 2 are distinct within the day
	day2IDs := make(map[string]bool)
	for _, a := range resDay2 {
		if day2IDs[a.ID] {
			t.Fatalf("duplicate asset %s in wraparound slice", a.ID)
		}
		day2IDs[a.ID] = true
	}
}

func TestSearchRandomAt_FullCoverageAcrossCycle(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Insert 150 assets in window
	baseTime := time.Date(2021, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 150; i++ {
		taken := baseTime.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-cov-%03d", i),
			RelPath:     fmt.Sprintf("2021/March/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2021,
			TakenMonth:  3,
			TakenDay:    1,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
	}

	startDate := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2021, 3, 31, 23, 59, 59, 0, time.UTC)

	// size = 50. Total = 150. Exactly 3 days (e.g. days 1, 2, 3) must cover 150/150 distinct items
	covered := make(map[string]bool)
	for day := 1; day <= 3; day++ {
		res, err := st.SearchRandomAt(ctx, &startDate, &endDate, 50, day)
		if err != nil {
			t.Fatalf("day %d failed: %v", day, err)
		}
		if len(res) != 50 {
			t.Fatalf("day %d: expected 50 items, got %d", day, len(res))
		}
		for _, a := range res {
			covered[a.ID] = true
		}
	}

	if len(covered) != 150 {
		t.Fatalf("expected 100%% coverage (150 distinct items) in 3 days, got %d", len(covered))
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

	// Insert 10 assets
	now := time.Now()
	for i := 0; i < 10; i++ {
		_ = st.UpsertAsset(ctx, store.Asset{
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
	}

	// SearchRandom without date bounds should NOT mark dealt_at
	res, err := st.SearchRandom(ctx, nil, nil, 5)
	if err != nil || len(res) != 5 {
		t.Fatalf("stateless random failed: len %d, err %v", len(res), err)
	}

	// Verify dealt_at is still nil in the database
	for _, a := range res {
		dbAsset, err := st.GetAsset(ctx, a.ID)
		if err != nil {
			t.Fatalf("get asset: %v", err)
		}
		if dbAsset.DealtAt != nil {
			t.Fatalf("expected dealt_at to remain nil for stateless random search")
		}
	}
}

