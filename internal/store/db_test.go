package store

import (
	"os"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	f, err := os.CreateTemp("", "go-bt-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := New(f.Name())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSaveAndGet(t *testing.T) {
	db := newTestDB(t)

	var ih [20]byte
	copy(ih[:], "12345678901234567890")

	if err := db.Save(ih, "Test Torrent", 1024*1024, 3); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := db.Get("3132333435363738393031323334353637383930")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected a torrent, got nil")
	}
	if got.Name != "Test Torrent" {
		t.Errorf("Name: got %q, want %q", got.Name, "Test Torrent")
	}
	if got.Size != 1024*1024 {
		t.Errorf("Size: got %d, want %d", got.Size, 1024*1024)
	}
	if got.FileCount != 3 {
		t.Errorf("FileCount: got %d, want %d", got.FileCount, 3)
	}
}

func TestSaveDuplicate(t *testing.T) {
	db := newTestDB(t)

	var ih [20]byte
	copy(ih[:], "aaaabbbbccccddddeeee")

	if err := db.Save(ih, "First", 100, 1); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// Second save with same infohash should be silently ignored.
	if err := db.Save(ih, "Second", 200, 2); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := db.Get("6161616162626262636363636464646465656565")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "First" {
		t.Errorf("expected first record to be kept, got %q", got.Name)
	}
}

func TestSearch(t *testing.T) {
	db := newTestDB(t)

	seeds := []struct {
		id   byte
		name string
	}{
		{1, "Ubuntu Linux ISO"},
		{2, "Debian Linux DVD"},
		{3, "Arch Linux Minimal"},
	}
	for _, s := range seeds {
		var ih [20]byte
		ih[0] = s.id
		if err := db.Save(ih, s.name, 700*1024*1024, 1); err != nil {
			t.Fatalf("Save %q: %v", s.name, err)
		}
	}

	result, err := db.Search("Linux", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("Total: got %d, want 3", result.Total)
	}

	result, err = db.Search("Ubuntu", 1, 10)
	if err != nil {
		t.Fatalf("Search Ubuntu: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("Total: got %d, want 1", result.Total)
	}
	if result.Items[0].Name != "Ubuntu Linux ISO" {
		t.Errorf("Name: got %q", result.Items[0].Name)
	}
}

func TestSearchEmpty(t *testing.T) {
	db := newTestDB(t)

	result, err := db.Search("", 1, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 results, got %d", result.Total)
	}
}

func TestCount(t *testing.T) {
	db := newTestDB(t)

	for i := 0; i < 5; i++ {
		var ih [20]byte
		ih[0] = byte(i)
		if err := db.Save(ih, "torrent", 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("Count: got %d, want 5", n)
	}
}
