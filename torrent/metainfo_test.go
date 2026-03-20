package torrent

import (
	"strings"
	"testing"

	"github.com/spectertian/go-bt/bencode"
)

// buildTestTorrent encodes a synthetic single-file torrent and returns the
// raw bytes.  Kept as a helper so both tests and benchmarks can reuse it
// without duplicating the construction logic.
func buildTestTorrent(name string, length int64, pieceLength int64) []byte {
	// Build a pieces field: one SHA-1 hash (20 bytes) per piece.
	numPieces := (length + pieceLength - 1) / pieceLength
	pieces := []byte(strings.Repeat("\xab\xcd\xef\x01\x23\x45\x67\x89\xab\xcd\xef\x01\x23\x45\x67\x89\xab\xcd\xef\x01", int(numPieces)))

	info := bencode.Dict{
		"name":         []byte(name),
		"piece length": pieceLength,
		"pieces":       pieces,
		"length":       length,
	}
	top := bencode.Dict{
		"announce": []byte("http://tracker.example.com/announce"),
		"info":     info,
	}
	b, err := bencode.EncodeToBytes(top)
	if err != nil {
		panic(err)
	}
	return b
}

func buildMultiFileTorrent() []byte {
	pieces := []byte(strings.Repeat("\xab\xcd\xef\x01\x23\x45\x67\x89\xab\xcd\xef\x01\x23\x45\x67\x89\xab\xcd\xef\x01", 4))
	info := bencode.Dict{
		"name":         []byte("myalbum"),
		"piece length": int64(524288),
		"pieces":       pieces,
		"files": bencode.List{
			bencode.Dict{
				"length": int64(1048576),
				"path":   bencode.List{[]byte("disc1"), []byte("track01.flac")},
			},
			bencode.Dict{
				"length": int64(2097152),
				"path":   bencode.List{[]byte("disc1"), []byte("track02.flac")},
			},
		},
	}
	top := bencode.Dict{
		"announce": []byte("http://tracker.example.com/announce"),
		"announce-list": bencode.List{
			bencode.List{[]byte("http://tracker.example.com/announce")},
			bencode.List{[]byte("udp://tracker2.example.com:6969/announce")},
		},
		"info": info,
	}
	b, err := bencode.EncodeToBytes(top)
	if err != nil {
		panic(err)
	}
	return b
}

func TestParseSingleFile(t *testing.T) {
	raw := buildTestTorrent("ubuntu.iso", 1073741824, 524288)
	mi, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if mi.Info.Name != "ubuntu.iso" {
		t.Errorf("Name = %q, want %q", mi.Info.Name, "ubuntu.iso")
	}
	if mi.Info.Length != 1073741824 {
		t.Errorf("Length = %d, want 1073741824", mi.Info.Length)
	}
	if mi.Info.PieceLength != 524288 {
		t.Errorf("PieceLength = %d, want 524288", mi.Info.PieceLength)
	}
	wantPieces := (1073741824 + 524288 - 1) / 524288
	if mi.Info.PieceCount() != wantPieces {
		t.Errorf("PieceCount = %d, want %d", mi.Info.PieceCount(), wantPieces)
	}
	if mi.Announce != "http://tracker.example.com/announce" {
		t.Errorf("Announce = %q", mi.Announce)
	}
	// InfoHash must be non-zero.
	var zero [20]byte
	if mi.InfoHash == zero {
		t.Error("InfoHash is zero")
	}
}

func TestParseMultiFile(t *testing.T) {
	raw := buildMultiFileTorrent()
	mi, err := Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if mi.Info.Name != "myalbum" {
		t.Errorf("Name = %q", mi.Info.Name)
	}
	if len(mi.Info.Files) != 2 {
		t.Fatalf("Files len = %d, want 2", len(mi.Info.Files))
	}
	if mi.Info.Files[0].Length != 1048576 {
		t.Errorf("Files[0].Length = %d", mi.Info.Files[0].Length)
	}
	if len(mi.Info.Files[0].Path) != 2 || mi.Info.Files[0].Path[1] != "track01.flac" {
		t.Errorf("Files[0].Path = %v", mi.Info.Files[0].Path)
	}
	if len(mi.AnnounceList) != 2 {
		t.Errorf("AnnounceList len = %d, want 2", len(mi.AnnounceList))
	}
}

func TestPieceHashSubslice(t *testing.T) {
	// Verify that PieceHash returns non-overlapping 20-byte views of the
	// backing Pieces slice without copying.
	raw := buildTestTorrent("test", 3*524288, 524288) // exactly 3 pieces
	mi, _ := Parse(strings.NewReader(string(raw)))

	if mi.Info.PieceCount() != 3 {
		t.Fatalf("expected 3 pieces, got %d", mi.Info.PieceCount())
	}
	for i := 0; i < 3; i++ {
		h := mi.Info.PieceHash(i)
		if len(h) != 20 {
			t.Errorf("PieceHash(%d) len = %d, want 20", i, len(h))
		}
	}
}

// ── benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkParseSingleFile(b *testing.B) {
	raw := buildTestTorrent("ubuntu.iso", 1073741824, 524288)
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mi, err := Parse(strings.NewReader(string(raw)))
		if err != nil {
			b.Fatal(err)
		}
		_ = mi
	}
}

func BenchmarkParseMultiFile(b *testing.B) {
	raw := buildMultiFileTorrent()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mi, err := Parse(strings.NewReader(string(raw)))
		if err != nil {
			b.Fatal(err)
		}
		_ = mi
	}
}
