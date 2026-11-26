package scanner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	_ "modernc.org/sqlite"

	"photo-api/internal/store"
)

// buildMinimalBigEndianEXIF constructs a valid big-endian TIFF EXIF payload
// ("Exif\x00\x00MM\x00*...") containing Orientation=6 and DateTimeOriginal="2025:12:28 11:35:56".
func buildMinimalBigEndianEXIF() []byte {
	const dateStr = "2025:12:28 11:35:56\x00" // 20 bytes
	var buf bytes.Buffer
	buf.WriteString("Exif\x00\x00")

	// TIFF header (offset 0 inside TIFF): "MM", 0x002a, IFD0 offset = 8
	tiffStart := buf.Len()
	buf.Write([]byte{'M', 'M', 0x00, 0x2a})
	_ = binary.Write(&buf, binary.BigEndian, uint32(8))

	// IFD0 at TIFF offset 8: 2 entries (Orientation=0x0112, ExifIFDPointer=0x8769)
	_ = binary.Write(&buf, binary.BigEndian, uint16(2))
	// Entry 1: Orientation (0x0112), SHORT (3), count 1, value 6 left-aligned in 4 bytes
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&buf, binary.BigEndian, uint16(3))
	_ = binary.Write(&buf, binary.BigEndian, uint32(1))
	_ = binary.Write(&buf, binary.BigEndian, uint16(6))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	// Entry 2: ExifIFDPointer (0x8769), LONG (4), count 1, offset = 8 + 2 + 24 + 4 = 38
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x8769))
	_ = binary.Write(&buf, binary.BigEndian, uint16(4))
	_ = binary.Write(&buf, binary.BigEndian, uint32(1))
	_ = binary.Write(&buf, binary.BigEndian, uint32(38))
	// Next IFD offset = 0
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))

	// Exif SubIFD at TIFF offset 38: 1 entry (DateTimeOriginal=0x9003)
	_ = binary.Write(&buf, binary.BigEndian, uint16(1))
	// Entry 1: DateTimeOriginal (0x9003), ASCII (2), count 20, offset = 38 + 2 + 12 + 4 = 56
	_ = binary.Write(&buf, binary.BigEndian, uint16(0x9003))
	_ = binary.Write(&buf, binary.BigEndian, uint16(2))
	_ = binary.Write(&buf, binary.BigEndian, uint32(uint32(len(dateStr))))
	_ = binary.Write(&buf, binary.BigEndian, uint32(56))
	// Next IFD offset = 0
	_ = binary.Write(&buf, binary.BigEndian, uint32(0))

	if got := buf.Len() - tiffStart; got != 56 {
		panic("unexpected TIFF offset")
	}
	buf.WriteString(dateStr)
	return buf.Bytes()
}

func TestDecodeHEICEXIF_SkipsInfeBoxFalsePositive(t *testing.T) {
	var fakeHEIC bytes.Buffer
	fakeHEIC.Write(make([]byte, 512))
	// Mimic iPhone HEIC iinf/infe item-info box containing "Exif\x00\x00" NOT followed by TIFF magic
	fakeHEIC.Write([]byte("Exif\x00\x00\x00\x00)infe\x02\x00\x00"))
	fakeHEIC.Write(make([]byte, 4096))
	fakeHEIC.Write(buildMinimalBigEndianEXIF())

	x := decodeHEICEXIF(&fakeHEIC)
	if x == nil {
		t.Fatalf("expected decodeHEICEXIF to skip infe box and parse real TIFF EXIF, got nil")
	}

	gotDate := extractEXIFDate(x)
	wantDate := time.Date(2025, 12, 28, 11, 35, 56, 0, time.UTC)
	if !gotDate.Equal(wantDate) {
		t.Fatalf("DateTimeOriginal = %v, want %v", gotDate, wantDate)
	}

	orientTag, err := x.Get(exif.Orientation)
	if err != nil {
		t.Fatalf("Orientation tag missing: %v", err)
	}
	orient, err := orientTag.Int(0)
	if err != nil || orient != 6 {
		t.Fatalf("Orientation = %d (err=%v), want 6", orient, err)
	}
}

func TestHEICReindexMigrationUpdatesExistingRows(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	libDir := filepath.Join(tmp, "photos")
	dataDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	heicPath := filepath.Join(libDir, "IMG_1747.HEIC")
	var content bytes.Buffer
	content.Write([]byte("Exif\x00\x00\x00\x00)infe\x02\x00\x00"))
	content.Write(make([]byte, 1024))
	content.Write(buildMinimalBigEndianEXIF())
	if err := os.WriteFile(heicPath, content.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(heicPath)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Seed a database row matching file mtime/size, then reset PRAGMA user_version = 0
	// to simulate an existing pre-fix NAS database.
	stInit, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	staleAsset := store.Asset{
		ID:          DeterministicUUID("IMG_1747.HEIC"),
		RelPath:     "IMG_1747.HEIC",
		MediaType:   "IMAGE",
		Mtime:       info.ModTime().Unix(),
		Size:        info.Size(),
		TakenAt:     time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
		TakenYear:   2026,
		TakenMonth:  10,
		TakenDay:    31,
		Orientation: 1,
		IndexedAt:   time.Now().UTC(),
	}
	if err := stInit.UpsertAsset(ctx, staleAsset); err != nil {
		t.Fatal(err)
	}
	_ = stInit.Close()

	rawDB, err := sql.Open("sqlite", filepath.Join(dataDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	_ = rawDB.Close()

	// 2. Reopen store (triggers user_version 0 -> 1 migration invalidating .HEIC mtime) and scan.
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sc := New(libDir, "", nil, st, nil)
	stats, err := sc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Updated != 1 {
		t.Fatalf("stats.Updated = %d, want 1 (existing HEIC row re-indexed)", stats.Updated)
	}

	got, err := st.GetAsset(ctx, DeterministicUUID("IMG_1747.HEIC"))
	if err != nil || got == nil {
		t.Fatalf("GetAsset failed: %v", err)
	}
	wantDate := time.Date(2025, 12, 28, 11, 35, 56, 0, time.UTC)
	if !got.TakenAt.Equal(wantDate) || got.Orientation != 6 {
		t.Fatalf("got TakenAt=%v Orientation=%d, want %v and 6", got.TakenAt, got.Orientation, wantDate)
	}
}

func TestRealHEICFileIfProvided(t *testing.T) {
	p := os.Getenv("REAL_HEIC")
	if p == "" {
		t.Skip("REAL_HEIC not set")
	}
	x := decodeEXIF(p)
	if x == nil {
		t.Fatalf("decodeEXIF(%s) returned nil", p)
	}
	lat, lon, err := x.LatLong()
	if err != nil {
		t.Fatalf("LatLong failed: %v", err)
	}
	dt := extractEXIFDate(x)
	t.Logf("Parsed real HEIC: lat=%.6f lon=%.6f date=%s", lat, lon, dt.Format(time.RFC3339))
}
