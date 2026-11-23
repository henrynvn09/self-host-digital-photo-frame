package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"photo-api/internal/store"
)

func TestSearchRandomAnniversaryRange(t *testing.T) {
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

	// SearchRandom with size=100
	results1, err := st.SearchRandom(ctx, &startDate, &endDate, 100)
	if err != nil {
		t.Fatalf("SearchRandom failed: %v", err)
	}

	if len(results1) != 100 {
		t.Fatalf("expected 100 items, got %d", len(results1))
	}

	for _, a := range results1 {
		if a.TakenYear != 2018 || a.TakenMonth < 10 || a.TakenMonth > 11 {
			t.Fatalf("asset %s out of date range: %v", a.ID, a.TakenAt)
		}
		if a.DealtAt == nil {
			t.Fatalf("expected dealt_at to be populated for returned asset %s", a.ID)
		}
	}

	// Query 2: size=100. Must return 100 completely disjoint assets (without replacement!)
	results2, err := st.SearchRandom(ctx, &startDate, &endDate, 100)
	if err != nil {
		t.Fatalf("SearchRandom 2 failed: %v", err)
	}
	if len(results2) != 100 {
		t.Fatalf("expected 100 items in second query, got %d", len(results2))
	}

	seenInFirst := make(map[string]bool)
	for _, a := range results1 {
		seenInFirst[a.ID] = true
	}
	for _, a := range results2 {
		if seenInFirst[a.ID] {
			t.Fatalf("asset %s was returned in both query 1 and query 2 (replacement occurred!)", a.ID)
		}
	}

	// Query 3: size=100. Only 50 unseen assets remain (250 total - 100 - 100).
	// Under-fill rule: Must return remaining 50 items without duplicates or errors!
	results3, err := st.SearchRandom(ctx, &startDate, &endDate, 100)
	if err != nil {
		t.Fatalf("SearchRandom 3 failed: %v", err)
	}
	if len(results3) != 50 {
		t.Fatalf("expected under-fill of exactly 50 remaining items, got %d", len(results3))
	}
	for _, a := range results3 {
		if seenInFirst[a.ID] {
			t.Fatalf("asset %s in query 3 overlapped with query 1", a.ID)
		}
	}

	// Query 4: Unseen pool was exhausted (0 remaining).
	// Auto-Reset rule: Deck should automatically reset and return 100 items from freshly shuffled cycle!
	results4, err := st.SearchRandom(ctx, &startDate, &endDate, 100)
	if err != nil {
		t.Fatalf("SearchRandom 4 (auto-reset) failed: %v", err)
	}
	if len(results4) != 100 {
		t.Fatalf("expected 100 items after auto-reset, got %d", len(results4))
	}
}

func TestSearchRandomYearIsolation(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// Year 2017: 30 assets
	time2017 := time.Date(2017, 10, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		taken := time2017.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-2017-%02d", i),
			RelPath:     fmt.Sprintf("2017/photo_%02d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2017,
			TakenMonth:  10,
			TakenDay:    15,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
	}

	// Year 2018: 150 assets
	time2018 := time.Date(2018, 10, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 150; i++ {
		taken := time2018.Add(time.Duration(i) * time.Hour)
		_ = st.UpsertAsset(ctx, store.Asset{
			ID:          fmt.Sprintf("asset-2018-%03d", i),
			RelPath:     fmt.Sprintf("2018/photo_%03d.jpg", i),
			Mtime:       taken.Unix(),
			Size:        1000,
			TakenAt:     taken,
			TakenYear:   2018,
			TakenMonth:  10,
			TakenDay:    15,
			Orientation: 1,
			IndexedAt:   time.Now(),
		})
	}

	start2017 := time.Date(2017, 10, 1, 0, 0, 0, 0, time.UTC)
	end2017 := time.Date(2017, 10, 31, 23, 59, 59, 0, time.UTC)

	start2018 := time.Date(2018, 10, 1, 0, 0, 0, 0, time.UTC)
	end2018 := time.Date(2018, 10, 31, 23, 59, 59, 0, time.UTC)

	// Query 2017: size=30 exhausts all 30 assets in 2017
	res2017_1, err := st.SearchRandom(ctx, &start2017, &end2017, 30)
	if err != nil || len(res2017_1) != 30 {
		t.Fatalf("expected 30 items for 2017, got %d (err: %v)", len(res2017_1), err)
	}

	// Query 2017 again: auto-resets and returns 30 items
	res2017_2, err := st.SearchRandom(ctx, &start2017, &end2017, 30)
	if err != nil || len(res2017_2) != 30 {
		t.Fatalf("expected 30 items on 2017 reset, got %d (err: %v)", len(res2017_2), err)
	}

	// Now query 2018: size=100. Year 2017 exhaustion should have ZERO effect on 2018!
	res2018_1, err := st.SearchRandom(ctx, &start2018, &end2018, 100)
	if err != nil || len(res2018_1) != 100 {
		t.Fatalf("expected 100 items for 2018, got %d (err: %v)", len(res2018_1), err)
	}

	// Next query 2018: should return remaining 50 unseen items
	res2018_2, err := st.SearchRandom(ctx, &start2018, &end2018, 100)
	if err != nil || len(res2018_2) != 50 {
		t.Fatalf("expected 50 remaining unseen items for 2018, got %d (err: %v)", len(res2018_2), err)
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
