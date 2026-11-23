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

	// Insert 250 assets in Year 2018 (Oct - Nov), and 50 assets in Year 2018 outside range (May), and 100 in 2019
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
	}

	// Perform a second query and verify randomized ordering / sampling
	results2, err := st.SearchRandom(ctx, &startDate, &endDate, 100)
	if err != nil {
		t.Fatalf("SearchRandom 2 failed: %v", err)
	}

	// Because results are sampled randomly with ORDER BY RANDOM(), the first item or order should differ
	different := false
	for i := 0; i < len(results1) && i < len(results2); i++ {
		if results1[i].ID != results2[i].ID {
			different = true
			break
		}
	}
	if !different {
		t.Fatalf("expected different ordering or random selection across calls")
	}
}
