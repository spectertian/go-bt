// Package torrent provides types and functions for parsing BitTorrent metainfo
// (.torrent) files.
//
// Performance notes:
//   - Parsing reuses the bencode.Decode path which avoids loading the entire
//     file into memory before work begins (streaming decode via bufio.Reader).
//   - The SHA-1 piece hashes stored in the "pieces" field are split into a
//     [][]byte where each element is a 20-byte sub-slice of the original
//     backing array, so no extra copying occurs.
//   - Fields that are not needed for a basic download (e.g., "comment",
//     "created by") are intentionally omitted to keep the hot struct small and
//     cache-friendly.
package torrent

import (
	"crypto/sha1"
	"fmt"
	"io"

	"github.com/spectertian/go-bt/bencode"
)

const pieceHashSize = 20 // SHA-1 digest length

// MetaInfo represents the top-level structure of a .torrent file.
type MetaInfo struct {
	Announce     string
	AnnounceList [][]string // multi-tracker extension (BEP 12)
	Info         Info
	// InfoHash is the SHA-1 hash of the bencoded info dict, used as the
	// torrent's unique identifier.
	InfoHash [pieceHashSize]byte
}

// Info represents the "info" dictionary inside a .torrent file.
type Info struct {
	Name        string
	PieceLength int64
	// Pieces holds the concatenated SHA-1 hashes of all pieces as a single
	// []byte to avoid N separate allocations.  Use PieceHash(i) to access
	// individual hashes.
	Pieces      []byte
	// Single-file mode
	Length int64
	// Multi-file mode
	Files []FileInfo
}

// FileInfo describes one file in a multi-file torrent.
type FileInfo struct {
	Length int64
	Path   []string
}

// PieceCount returns the number of pieces in the torrent.
func (info *Info) PieceCount() int {
	return len(info.Pieces) / pieceHashSize
}

// PieceHash returns the expected SHA-1 hash of piece i as a 20-byte slice.
// The returned slice is a sub-slice of Info.Pieces; callers must not modify it.
func (info *Info) PieceHash(i int) []byte {
	off := i * pieceHashSize
	return info.Pieces[off : off+pieceHashSize]
}

// Parse reads and parses a .torrent file from r.
func Parse(r io.Reader) (*MetaInfo, error) {
	raw, err := bencode.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("torrent: bencode decode: %w", err)
	}
	top, ok := raw.(bencode.Dict)
	if !ok {
		return nil, fmt.Errorf("torrent: top-level value is not a dict")
	}

	mi := &MetaInfo{}

	if v, ok := top["announce"]; ok {
		mi.Announce = string(mustBytes(v))
	}

	if v, ok := top["announce-list"]; ok {
		mi.AnnounceList = parseAnnounceList(v)
	}

	infoRaw, ok := top["info"]
	if !ok {
		return nil, fmt.Errorf("torrent: missing 'info' key")
	}
	infoDict, ok := infoRaw.(bencode.Dict)
	if !ok {
		return nil, fmt.Errorf("torrent: 'info' is not a dict")
	}

	// Compute InfoHash: re-encode the info dict and SHA-1 hash it.
	// This is cheaper than storing the raw bytes of the entire file and
	// slicing, because we only need the info section.
	infoEncoded, err := bencode.EncodeToBytes(infoDict)
	if err != nil {
		return nil, fmt.Errorf("torrent: re-encode info dict: %w", err)
	}
	mi.InfoHash = sha1.Sum(infoEncoded)

	info, err := parseInfo(infoDict)
	if err != nil {
		return nil, err
	}
	mi.Info = *info
	return mi, nil
}

// parseInfo populates an Info from a decoded bencode Dict.
func parseInfo(d bencode.Dict) (*Info, error) {
	info := &Info{}

	if v, ok := d["name"]; ok {
		info.Name = string(mustBytes(v))
	}
	if v, ok := d["piece length"]; ok {
		info.PieceLength = mustInt(v)
	}
	if v, ok := d["pieces"]; ok {
		info.Pieces = mustBytes(v)
		if len(info.Pieces)%pieceHashSize != 0 {
			return nil, fmt.Errorf("torrent: pieces length %d is not a multiple of %d",
				len(info.Pieces), pieceHashSize)
		}
	}

	// Single-file mode
	if v, ok := d["length"]; ok {
		info.Length = mustInt(v)
		return info, nil
	}

	// Multi-file mode
	if v, ok := d["files"]; ok {
		lst, ok := v.(bencode.List)
		if !ok {
			return nil, fmt.Errorf("torrent: 'files' is not a list")
		}
		// Pre-allocate the exact number of FileInfo entries.
		info.Files = make([]FileInfo, 0, len(lst))
		for _, item := range lst {
			fd, ok := item.(bencode.Dict)
			if !ok {
				return nil, fmt.Errorf("torrent: file entry is not a dict")
			}
			fi, err := parseFileInfo(fd)
			if err != nil {
				return nil, err
			}
			info.Files = append(info.Files, fi)
		}
	}
	return info, nil
}

// parseFileInfo populates a FileInfo from a decoded bencode Dict.
func parseFileInfo(d bencode.Dict) (FileInfo, error) {
	fi := FileInfo{}
	if v, ok := d["length"]; ok {
		fi.Length = mustInt(v)
	}
	if v, ok := d["path"]; ok {
		lst, ok := v.(bencode.List)
		if !ok {
			return fi, fmt.Errorf("torrent: file 'path' is not a list")
		}
		// Pre-allocate the exact number of path components.
		fi.Path = make([]string, 0, len(lst))
		for _, p := range lst {
			fi.Path = append(fi.Path, string(mustBytes(p)))
		}
	}
	return fi, nil
}

// parseAnnounceList parses the BEP-12 "announce-list" field.
func parseAnnounceList(v bencode.Value) [][]string {
	outer, ok := v.(bencode.List)
	if !ok {
		return nil
	}
	// Pre-allocate the outer slice with the exact tier count.
	result := make([][]string, 0, len(outer))
	for _, tier := range outer {
		inner, ok := tier.(bencode.List)
		if !ok {
			continue
		}
		// Pre-allocate the inner slice with the exact tracker count.
		trackers := make([]string, 0, len(inner))
		for _, t := range inner {
			trackers = append(trackers, string(mustBytes(t)))
		}
		result = append(result, trackers)
	}
	return result
}

// mustBytes returns the []byte value of v, or nil if v is not a []byte.
// Inline-friendly helper that avoids a type-assertion on the caller side.
func mustBytes(v bencode.Value) []byte {
	b, _ := v.([]byte)
	return b
}

// mustInt returns the int64 value of v, or 0 if v is not an int64.
func mustInt(v bencode.Value) int64 {
	n, _ := v.(int64)
	return n
}
