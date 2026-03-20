package bencode

import (
	"encoding/hex"
	"testing"
)

func TestDecodeInt(t *testing.T) {
	v, err := Decode([]byte("i42e"))
	if err != nil {
		t.Fatal(err)
	}
	if v.(int64) != 42 {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestDecodeString(t *testing.T) {
	v, err := Decode([]byte("4:spam"))
	if err != nil {
		t.Fatal(err)
	}
	if string(v.([]byte)) != "spam" {
		t.Errorf("expected spam, got %s", v)
	}
}

func TestDecodeList(t *testing.T) {
	v, err := Decode([]byte("l4:spami42ee"))
	if err != nil {
		t.Fatal(err)
	}
	lst := v.([]interface{})
	if len(lst) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(lst))
	}
	if string(lst[0].([]byte)) != "spam" {
		t.Errorf("expected spam, got %s", lst[0])
	}
	if lst[1].(int64) != 42 {
		t.Errorf("expected 42, got %v", lst[1])
	}
}

func TestDecodeDict(t *testing.T) {
	v, err := Decode([]byte("d3:cow3:moo4:spam4:eggse"))
	if err != nil {
		t.Fatal(err)
	}
	d := v.(map[string]interface{})
	if string(d["cow"].([]byte)) != "moo" {
		t.Errorf("expected moo, got %s", d["cow"])
	}
	if string(d["spam"].([]byte)) != "eggs" {
		t.Errorf("expected eggs, got %s", d["spam"])
	}
}

func TestEncodeDecode(t *testing.T) {
	original := map[string]interface{}{
		"t": []byte("aa"),
		"y": []byte("q"),
		"q": []byte("ping"),
		"a": map[string]interface{}{
			"id": []byte("abcdefghij0123456789"),
		},
	}
	encoded, err := Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDict(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v (encoded: %s)", err, hex.EncodeToString(encoded))
	}
	if string(decoded["y"].([]byte)) != "q" {
		t.Errorf("expected q, got %s", decoded["y"])
	}
}

func TestGetHelpers(t *testing.T) {
	d := map[string]interface{}{
		"name":   []byte("test"),
		"size":   int64(1024),
		"nested": map[string]interface{}{"k": []byte("v")},
		"list":   []interface{}{[]byte("a")},
	}
	if s, ok := GetString(d, "name"); !ok || s != "test" {
		t.Errorf("GetString failed: %s %v", s, ok)
	}
	if n, ok := GetInt64(d, "size"); !ok || n != 1024 {
		t.Errorf("GetInt64 failed: %d %v", n, ok)
	}
	if m, ok := GetDict(d, "nested"); !ok || m == nil {
		t.Errorf("GetDict failed")
	}
	if l, ok := GetList(d, "list"); !ok || len(l) != 1 {
		t.Errorf("GetList failed")
	}
}
