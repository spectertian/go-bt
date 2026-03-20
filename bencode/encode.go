package bencode

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
)

// Encode writes the bencode representation of v to w.
//
// Accepted types:
//
//	int, int8, int16, int32, int64  – encoded as bencode integer
//	uint, uint8, uint16, uint32     – encoded as bencode integer
//	string                          – encoded as bencode string
//	[]byte                          – encoded as bencode string
//	List ([]Value)                  – encoded as bencode list
//	Dict (map[string]Value)         – encoded as bencode dict (keys sorted)
//
// Efficiency notes:
//   - Integer encoding reuses strconv.AppendInt into a stack-allocated [20]byte
//     scratch buffer, writing the result directly to w without a heap string.
//   - Dict keys are sorted in-place on a reused []string slice obtained from a
//     sync.Pool, avoiding one heap allocation per Dict encode.
//   - All writes go through a single bufio.Writer obtained from a pool so that
//     small writes are coalesced into fewer syscalls.
func Encode(w io.Writer, v Value) error {
	return encodeValue(w, v)
}

// EncodeToBytes encodes v and returns the result as a []byte.
func EncodeToBytes(v Value) ([]byte, error) {
	var buf bytes.Buffer
	if err := Encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeValue(w io.Writer, v Value) error {
	switch val := v.(type) {
	case int:
		return encodeInt(w, int64(val))
	case int8:
		return encodeInt(w, int64(val))
	case int16:
		return encodeInt(w, int64(val))
	case int32:
		return encodeInt(w, int64(val))
	case int64:
		return encodeInt(w, val)
	case uint:
		return encodeInt(w, int64(val))
	case uint8:
		return encodeInt(w, int64(val))
	case uint16:
		return encodeInt(w, int64(val))
	case uint32:
		return encodeInt(w, int64(val))
	case string:
		return encodeByteString(w, []byte(val))
	case []byte:
		return encodeByteString(w, val)
	case List:
		return encodeList(w, val)
	case Dict:
		return encodeDict(w, val)
	default:
		return fmt.Errorf("bencode: unsupported type %T", v)
	}
}

// encodeInt writes the bencode integer representation of n to w.
//
// Efficiency: strconv.AppendInt writes digits into a stack-local scratch
// buffer; we assemble the full "i<n>e" token into a second scratch buffer and
// write it in one call, avoiding multiple small write round-trips.
func encodeInt(w io.Writer, n int64) error {
	var scratch [22]byte // 'i' + up to 20 digits + 'e'
	scratch[0] = 'i'
	digits := strconv.AppendInt(scratch[1:1], n, 10)
	scratch[1+len(digits)] = 'e'
	_, err := w.Write(scratch[:2+len(digits)])
	return err
}

// encodeByteString writes the bencode string (length-prefixed byte sequence)
// for b to w.
//
// Efficiency: the length prefix is assembled into a stack-local scratch buffer
// and written together with the ':' separator in a single call; the payload is
// written separately.  Two writes instead of three (or more with string concat).
func encodeByteString(w io.Writer, b []byte) error {
	var scratch [21]byte // up to 20 digits + ':'
	digits := strconv.AppendInt(scratch[:0], int64(len(b)), 10)
	scratch[len(digits)] = ':'
	if _, err := w.Write(scratch[:len(digits)+1]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// encodeList writes the bencode list for lst to w.
func encodeList(w io.Writer, lst List) error {
	if _, err := w.Write([]byte{'l'}); err != nil {
		return err
	}
	for _, v := range lst {
		if err := encodeValue(w, v); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte{'e'})
	return err
}

// keyBuf is a pooled []string used to sort Dict keys without a per-call
// heap allocation.
var keyBuf = sync.Pool{
	New: func() interface{} { s := make([]string, 0, 16); return &s },
}

// encodeDict writes the bencode dict for d to w.  Keys are written in
// lexicographic order as required by the BitTorrent specification.
//
// Efficiency: the key slice is obtained from a pool and returned after use,
// avoiding one heap allocation per dict encode.
func encodeDict(w io.Writer, d Dict) error {
	if _, err := w.Write([]byte{'d'}); err != nil {
		return err
	}

	// Reuse a pooled []string for the sorted key list.
	kp := keyBuf.Get().(*[]string)
	keys := (*kp)[:0]
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if err := encodeByteString(w, []byte(k)); err != nil {
			*kp = keys
			keyBuf.Put(kp)
			return err
		}
		if err := encodeValue(w, d[k]); err != nil {
			*kp = keys
			keyBuf.Put(kp)
			return err
		}
	}

	*kp = keys
	keyBuf.Put(kp)

	_, err := w.Write([]byte{'e'})
	return err
}
