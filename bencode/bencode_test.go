package bencode

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// ── unit tests ────────────────────────────────────────────────────────────────

func TestDecodeInteger(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"i0e", 0},
		{"i42e", 42},
		{"i-1e", -1},
		{"i1234567890e", 1234567890},
		{"i-9999e", -9999},
	}
	for _, tc := range tests {
		v, err := DecodeBytes([]byte(tc.input))
		if err != nil {
			t.Errorf("DecodeBytes(%q): %v", tc.input, err)
			continue
		}
		got, ok := v.(int64)
		if !ok {
			t.Errorf("DecodeBytes(%q): got %T, want int64", tc.input, v)
			continue
		}
		if got != tc.want {
			t.Errorf("DecodeBytes(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestDecodeString(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		{"4:spam", []byte("spam")},
		{"0:", []byte("")},
		{"11:hello world", []byte("hello world")},
	}
	for _, tc := range tests {
		v, err := DecodeBytes([]byte(tc.input))
		if err != nil {
			t.Errorf("DecodeBytes(%q): %v", tc.input, err)
			continue
		}
		got, ok := v.([]byte)
		if !ok {
			t.Errorf("DecodeBytes(%q): got %T, want []byte", tc.input, v)
			continue
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("DecodeBytes(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDecodeList(t *testing.T) {
	v, err := DecodeBytes([]byte("li1ei2ei3ee"))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	lst, ok := v.(List)
	if !ok {
		t.Fatalf("expected List, got %T", v)
	}
	want := List{int64(1), int64(2), int64(3)}
	if !reflect.DeepEqual(lst, want) {
		t.Errorf("got %v, want %v", lst, want)
	}
}

func TestDecodeDict(t *testing.T) {
	v, err := DecodeBytes([]byte("d3:bar4:spam3:fooi42ee"))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	d, ok := v.(Dict)
	if !ok {
		t.Fatalf("expected Dict, got %T", v)
	}
	if !bytes.Equal(d["bar"].([]byte), []byte("spam")) {
		t.Errorf("d[bar] = %q, want %q", d["bar"], "spam")
	}
	if d["foo"].(int64) != 42 {
		t.Errorf("d[foo] = %v, want 42", d["foo"])
	}
}

func TestDecodeNestedStructure(t *testing.T) {
	// d{files: [{length: 100, path: ["dir","file.txt"]}], name: "test"}
	input := "d5:filesl" +
		"d6:lengthi100e4:pathl3:dir8:file.txtee" +
		"e4:name4:teste"
	v, err := DecodeBytes([]byte(input))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	d, ok := v.(Dict)
	if !ok {
		t.Fatalf("expected Dict, got %T", v)
	}
	name, _ := d["name"].([]byte)
	if string(name) != "test" {
		t.Errorf("name = %q, want %q", name, "test")
	}
	files, _ := d["files"].(List)
	if len(files) != 1 {
		t.Fatalf("files len = %d, want 1", len(files))
	}
	fileDict, _ := files[0].(Dict)
	if fileDict["length"].(int64) != 100 {
		t.Errorf("files[0].length = %v, want 100", fileDict["length"])
	}
}

// ── encode tests ──────────────────────────────────────────────────────────────

func TestEncodeInteger(t *testing.T) {
	tests := []struct {
		v    Value
		want string
	}{
		{int64(0), "i0e"},
		{int64(42), "i42e"},
		{int64(-1), "i-1e"},
		{int(100), "i100e"},
	}
	for _, tc := range tests {
		got, err := EncodeToBytes(tc.v)
		if err != nil {
			t.Errorf("EncodeToBytes(%v): %v", tc.v, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("EncodeToBytes(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestEncodeString(t *testing.T) {
	tests := []struct {
		v    Value
		want string
	}{
		{[]byte("spam"), "4:spam"},
		{[]byte(""), "0:"},
		{"hello", "5:hello"},
	}
	for _, tc := range tests {
		got, err := EncodeToBytes(tc.v)
		if err != nil {
			t.Errorf("EncodeToBytes(%v): %v", tc.v, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("EncodeToBytes(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestEncodeDict(t *testing.T) {
	d := Dict{"foo": int64(42), "bar": []byte("spam")}
	got, err := EncodeToBytes(d)
	if err != nil {
		t.Fatalf("EncodeToBytes: %v", err)
	}
	// Dict keys must be in lexicographic order: bar < foo
	want := "d3:bar4:spam3:fooi42ee"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	// Encode then decode; the round-trip must reproduce the original structure.
	original := Dict{
		"name":   []byte("ubuntu.iso"),
		"length": int64(1073741824),
		"pieces": []byte(strings.Repeat("x", 40)),
		"info": Dict{
			"piece length": int64(524288),
		},
	}
	enc, err := EncodeToBytes(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeBytes(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	d, ok := decoded.(Dict)
	if !ok {
		t.Fatalf("expected Dict, got %T", decoded)
	}
	if string(d["name"].([]byte)) != "ubuntu.iso" {
		t.Errorf("name mismatch")
	}
	if d["length"].(int64) != 1073741824 {
		t.Errorf("length mismatch")
	}
}

// ── benchmarks ────────────────────────────────────────────────────────────────

// sampleTorrent is a synthetic bencode blob roughly resembling a small
// multi-file torrent's info dict: a dict with nested list and string fields.
var sampleTorrent = func() []byte {
	d := Dict{
		"name":         []byte("ubuntu-22.04-desktop-amd64.iso"),
		"piece length": int64(524288),
		"pieces":       []byte(strings.Repeat("\xab\xcd", 20*1000)), // 40 000 bytes
		"files": List{
			Dict{"length": int64(1073741824), "path": List{[]byte("ubuntu-22.04.iso")}},
			Dict{"length": int64(4096), "path": List{[]byte("md5sums")}},
		},
	}
	b, err := EncodeToBytes(d)
	if err != nil {
		panic(err)
	}
	return b
}()

// BenchmarkDecode measures the throughput of the decoder on a realistic
// torrent-info-dict payload.
func BenchmarkDecode(b *testing.B) {
	b.SetBytes(int64(len(sampleTorrent)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := DecodeBytes(sampleTorrent)
		if err != nil {
			b.Fatal(err)
		}
		_ = v
	}
}

// BenchmarkEncode measures the throughput of the encoder on the same payload.
func BenchmarkEncode(b *testing.B) {
	d, _ := DecodeBytes(sampleTorrent)
	b.SetBytes(int64(len(sampleTorrent)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := EncodeToBytes(d)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeSmallMessage simulates the frequent case of decoding
// individual peer-wire-protocol messages (e.g., have, request, piece metadata).
func BenchmarkDecodeSmallMessage(b *testing.B) {
	msg := []byte("d8:msg_typei1e5:piecei0ee") // small dict like a ut_metadata msg
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := DecodeBytes(msg)
		if err != nil {
			b.Fatal(err)
		}
		_ = v
	}
}
