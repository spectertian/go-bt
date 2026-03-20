// Package bencode implements encoding and decoding of bencoded data
// as defined in BEP 3 (BitTorrent Protocol Specification).
package bencode

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Decode parses bencoded data and returns a Go value.
// Strings are returned as []byte, integers as int64,
// lists as []interface{}, and dicts as map[string]interface{}.
func Decode(data []byte) (interface{}, error) {
	val, n, err := decode(data, 0)
	if err != nil {
		return nil, err
	}
	if n != len(data) {
		return nil, fmt.Errorf("trailing data after bencoded value at offset %d", n)
	}
	return val, nil
}

// DecodeDict is a convenience wrapper that decodes data and asserts the result
// is a map[string]interface{}.
func DecodeDict(data []byte) (map[string]interface{}, error) {
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	d, ok := v.(map[string]interface{})
	if !ok {
		return nil, errors.New("expected a bencoded dict at top level")
	}
	return d, nil
}

func decode(data []byte, pos int) (interface{}, int, error) {
	if pos >= len(data) {
		return nil, pos, errors.New("unexpected end of data")
	}
	switch {
	case data[pos] == 'i':
		return decodeInt(data, pos)
	case data[pos] == 'l':
		return decodeList(data, pos)
	case data[pos] == 'd':
		return decodeDict(data, pos)
	case data[pos] >= '0' && data[pos] <= '9':
		return decodeString(data, pos)
	default:
		return nil, pos, fmt.Errorf("unexpected character %q at offset %d", data[pos], pos)
	}
}

func decodeInt(data []byte, pos int) (int64, int, error) {
	pos++ // skip 'i'
	end := bytes.IndexByte(data[pos:], 'e')
	if end < 0 {
		return 0, pos, errors.New("unterminated integer")
	}
	n, err := strconv.ParseInt(string(data[pos:pos+end]), 10, 64)
	if err != nil {
		return 0, pos, err
	}
	return n, pos + end + 1, nil
}

func decodeString(data []byte, pos int) ([]byte, int, error) {
	colonPos := bytes.IndexByte(data[pos:], ':')
	if colonPos < 0 {
		return nil, pos, errors.New("invalid string: no colon found")
	}
	colonPos += pos
	length, err := strconv.Atoi(string(data[pos:colonPos]))
	if err != nil {
		return nil, pos, fmt.Errorf("invalid string length: %w", err)
	}
	start := colonPos + 1
	end := start + length
	if end > len(data) {
		return nil, pos, fmt.Errorf("string length %d exceeds data boundary", length)
	}
	// Return a copy to avoid retaining the full input buffer.
	result := make([]byte, length)
	copy(result, data[start:end])
	return result, end, nil
}

func decodeList(data []byte, pos int) ([]interface{}, int, error) {
	pos++ // skip 'l'
	var items []interface{}
	for pos < len(data) && data[pos] != 'e' {
		item, newPos, err := decode(data, pos)
		if err != nil {
			return nil, pos, err
		}
		items = append(items, item)
		pos = newPos
	}
	if pos >= len(data) {
		return nil, pos, errors.New("unterminated list")
	}
	return items, pos + 1, nil
}

func decodeDict(data []byte, pos int) (map[string]interface{}, int, error) {
	pos++ // skip 'd'
	m := make(map[string]interface{})
	for pos < len(data) && data[pos] != 'e' {
		// Keys must be byte strings.
		keyBytes, newPos, err := decodeString(data, pos)
		if err != nil {
			return nil, pos, fmt.Errorf("dict key: %w", err)
		}
		pos = newPos
		val, newPos, err := decode(data, pos)
		if err != nil {
			return nil, pos, fmt.Errorf("dict value for key %q: %w", keyBytes, err)
		}
		m[string(keyBytes)] = val
		pos = newPos
	}
	if pos >= len(data) {
		return nil, pos, errors.New("unterminated dict")
	}
	return m, pos + 1, nil
}

// Encode encodes a Go value to bencoded bytes.
// Supported types: string, []byte, int, int64, map[string]interface{}, []interface{}.
func Encode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(buf *bytes.Buffer, v interface{}) error {
	switch t := v.(type) {
	case string:
		buf.WriteString(strconv.Itoa(len(t)))
		buf.WriteByte(':')
		buf.WriteString(t)
	case []byte:
		buf.WriteString(strconv.Itoa(len(t)))
		buf.WriteByte(':')
		buf.Write(t)
	case int:
		fmt.Fprintf(buf, "i%de", t)
	case int64:
		fmt.Fprintf(buf, "i%de", t)
	case map[string]interface{}:
		buf.WriteByte('d')
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := encode(buf, k); err != nil {
				return err
			}
			if err := encode(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case []interface{}:
		buf.WriteByte('l')
		for _, item := range t {
			if err := encode(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("unsupported type %T", v)
	}
	return nil
}

// GetString is a helper to read a string field from a decoded dict.
func GetString(d map[string]interface{}, key string) (string, bool) {
	v, ok := d[key]
	if !ok {
		return "", false
	}
	b, ok := v.([]byte)
	if !ok {
		return "", false
	}
	return string(b), true
}

// GetBytes is a helper to read a []byte field from a decoded dict.
func GetBytes(d map[string]interface{}, key string) ([]byte, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	b, ok := v.([]byte)
	return b, ok
}

// GetDict is a helper to read a nested dict field.
func GetDict(d map[string]interface{}, key string) (map[string]interface{}, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	return m, ok
}

// GetInt64 is a helper to read an int64 field from a decoded dict.
func GetInt64(d map[string]interface{}, key string) (int64, bool) {
	v, ok := d[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(int64)
	return n, ok
}

// GetList is a helper to read a list field from a decoded dict.
func GetList(d map[string]interface{}, key string) ([]interface{}, bool) {
	v, ok := d[key]
	if !ok {
		return nil, false
	}
	l, ok := v.([]interface{})
	return l, ok
}
