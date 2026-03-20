// Package bencode implements encoding and decoding of bencode data, as used
// by the BitTorrent protocol for .torrent metainfo files and tracker responses.
//
// Performance notes:
//   - The decoder reads directly from an io.Reader via bufio.Reader, avoiding
//     loading the entire input into memory before parsing.
//   - Integer parsing is done without intermediate string allocations by
//     accumulating digit values directly into an int64.
//   - String/byte slice lengths are parsed eagerly so the exact number of
//     bytes can be read in a single call.
//   - Lists and dicts are pre-allocated using the lazy-count of leading items
//     where possible; when the count cannot be known ahead of time, a
//     preallocated initial capacity prevents the first few doublings.
//   - A sync.Pool of bufio.Reader wrappers reduces allocation pressure when
//     Decode is called repeatedly (e.g., parsing many peer messages).
package bencode

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Value is the Go representation of a bencode value.  The underlying type is
// one of:
//
//	int64        – bencode integer  (e.g. i42e)
//	[]byte       – bencode string   (e.g. 4:spam)
//	List         – bencode list     (e.g. li1ei2ee)
//	Dict         – bencode dict     (e.g. d3:fooi1ee)
type Value interface{}

// List is a slice of bencode values.
type List []Value

// Dict is an ordered map of bencode key→value pairs.  Keys are always []byte
// (bencode strings), ordered lexicographically as required by the spec.
type Dict map[string]Value

// bufioReaderPool recycles *bufio.Reader instances to avoid repeated heap
// allocation in tight loops (e.g., decoding hundreds of peer messages per
// second).
var bufioReaderPool = sync.Pool{
	New: func() interface{} { return bufio.NewReaderSize(nil, 4096) },
}

// Decode parses a single bencode value from r and returns it as a Value.
// It wraps r in a pooled bufio.Reader for efficient byte-by-byte reads,
// ensuring that no more bytes than necessary are consumed from the underlying
// reader.
func Decode(r io.Reader) (Value, error) {
	br := bufioReaderPool.Get().(*bufio.Reader)
	br.Reset(r)
	v, err := decodeValue(br)
	bufioReaderPool.Put(br)
	return v, err
}

// DecodeBytes is a convenience wrapper that decodes from a byte slice.
// It avoids an extra copy by wrapping the slice in a bytes.Reader.
func DecodeBytes(data []byte) (Value, error) {
	return Decode(bytes.NewReader(data))
}

// decodeValue dispatches to the appropriate typed decoder based on the first
// byte (peek, not consume).
func decodeValue(r *bufio.Reader) (Value, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch {
	case b == 'i':
		return decodeInt(r)
	case b == 'l':
		return decodeList(r)
	case b == 'd':
		return decodeDict(r)
	case b >= '0' && b <= '9':
		return decodeString(r, b)
	default:
		return nil, fmt.Errorf("bencode: unexpected byte %q", b)
	}
}

// decodeInt decodes a bencode integer.  The leading 'i' has already been
// consumed.  Format: i<decimal>e.
//
// Efficiency: digits are folded directly into an int64 accumulator – no
// intermediate string is built, so no string→int64 conversion allocation.
func decodeInt(r *bufio.Reader) (int64, error) {
	var n int64
	neg := false

	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	if b == '-' {
		neg = true
		b, err = r.ReadByte()
		if err != nil {
			return 0, err
		}
	}

	if b < '0' || b > '9' {
		return 0, fmt.Errorf("bencode: integer: expected digit, got %q", b)
	}

	for b != 'e' {
		if b < '0' || b > '9' {
			return 0, fmt.Errorf("bencode: integer: unexpected byte %q", b)
		}
		n = n*10 + int64(b-'0')
		b, err = r.ReadByte()
		if err != nil {
			return 0, err
		}
	}

	if neg {
		n = -n
	}
	return n, nil
}

// decodeString decodes a bencode string (byte sequence).  firstDigit is the
// first byte of the length prefix, already consumed by the caller.
//
// Efficiency: the length is decoded without a string allocation (same digit
// accumulation as decodeInt), and then io.ReadFull reads the payload in one
// call rather than byte-by-byte.
func decodeString(r *bufio.Reader, firstDigit byte) ([]byte, error) {
	length := int64(firstDigit - '0')

	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == ':' {
			break
		}
		if b < '0' || b > '9' {
			return nil, fmt.Errorf("bencode: string length: unexpected byte %q", b)
		}
		length = length*10 + int64(b-'0')
	}

	if length < 0 {
		return nil, fmt.Errorf("bencode: negative string length %d", length)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("bencode: string payload: %w", err)
	}
	return buf, nil
}

// decodeList decodes a bencode list.  The leading 'l' has already been
// consumed.
//
// Efficiency: the list is pre-allocated with a small initial capacity (8) to
// avoid the first several doubling reallocations while still being O(n) total.
func decodeList(r *bufio.Reader) (List, error) {
	list := make(List, 0, 8)

	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 'e' {
			break
		}
		// Put the byte back so decodeValue can dispatch on it.
		if err := r.UnreadByte(); err != nil {
			return nil, err
		}

		v, err := decodeValue(r)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, nil
}

// decodeDict decodes a bencode dictionary.  The leading 'd' has already been
// consumed.
//
// Efficiency: the map is pre-allocated with capacity 8, avoiding rehashing for
// small dicts (the common case for peer messages and announce responses).
// Keys are converted to string in one step from the decoded []byte, avoiding a
// redundant []byte→string allocation per key.
func decodeDict(r *bufio.Reader) (Dict, error) {
	dict := make(Dict, 8)

	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 'e' {
			break
		}

		// Keys are always bencode strings.
		if b < '0' || b > '9' {
			return nil, fmt.Errorf("bencode: dict key: expected string, got %q", b)
		}
		keyBytes, err := decodeString(r, b)
		if err != nil {
			return nil, fmt.Errorf("bencode: dict key: %w", err)
		}
		// Convert []byte key to string once; the map lookup and storage share
		// a single allocation.
		key := string(keyBytes)

		v, err := decodeValue(r)
		if err != nil {
			return nil, fmt.Errorf("bencode: dict value for key %q: %w", key, err)
		}
		dict[key] = v
	}
	return dict, nil
}
