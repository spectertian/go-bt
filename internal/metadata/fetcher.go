// Package metadata implements BitTorrent metadata fetching via BEP 9
// (Extension for Peers to Send Metadata Files) and BEP 10
// (Extension Protocol).
//
// Given an infohash, it connects to a DHT peer and downloads the torrent's
// info dictionary without downloading the full torrent.
package metadata

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"time"

	"github.com/spectertian/go-bt/internal/bencode"
)

const (
	handshakeTimeout = 15 * time.Second
	metadataTimeout  = 30 * time.Second
	metadataPieceLen = 16384 // 16 KiB per BEP 9
	maxMetadataSize  = 10 * 1024 * 1024 // 10 MiB sanity limit
)

// TorrentInfo holds the information extracted from a torrent's info dictionary.
type TorrentInfo struct {
	// InfoHash is the 20-byte SHA1 hash of the info dictionary.
	InfoHash [20]byte
	// Name is the display name of the torrent.
	Name string
	// Length is the total size in bytes (single-file torrents).
	Length int64
	// Files lists individual files for multi-file torrents.
	Files []FileInfo
}

// TotalSize returns the sum of all file sizes.
func (t *TorrentInfo) TotalSize() int64 {
	if t.Length > 0 {
		return t.Length
	}
	var sum int64
	for _, f := range t.Files {
		sum += f.Length
	}
	return sum
}

// FileInfo describes a single file within a torrent.
type FileInfo struct {
	Path   string
	Length int64
}

// Fetcher fetches torrent metadata from peers.
type Fetcher struct{}

// NewFetcher creates a new Fetcher.
func NewFetcher() *Fetcher { return &Fetcher{} }

// Fetch connects to addr and retrieves the info dictionary for infohash.
// addr must be a TCP-reachable peer that has the torrent (e.g. from an
// announce_peer DHT message).
func (f *Fetcher) Fetch(infohash [20]byte, addr string) (*TorrentInfo, error) {
	conn, err := net.DialTimeout("tcp4", addr, handshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(metadataTimeout))

	if err := sendHandshake(conn, infohash); err != nil {
		return nil, fmt.Errorf("handshake: %w", err)
	}
	peerID, extBits, err := recvHandshake(conn, infohash)
	_ = peerID
	if err != nil {
		return nil, fmt.Errorf("recv handshake: %w", err)
	}

	// Check BEP10 extension protocol bit (byte 5, bit 4 of reserved).
	if extBits[5]&0x10 == 0 {
		return nil, errors.New("peer does not support extension protocol (BEP10)")
	}

	// BEP10 extended handshake.
	metadataSize, peerExtID, err := exchangeExtHandshake(conn)
	if err != nil {
		return nil, fmt.Errorf("ext handshake: %w", err)
	}
	if peerExtID == 0 {
		return nil, errors.New("peer does not support ut_metadata (BEP9)")
	}
	if metadataSize <= 0 || metadataSize > maxMetadataSize {
		return nil, fmt.Errorf("invalid metadata_size: %d", metadataSize)
	}

	// Download metadata pieces.
	raw, err := downloadMetadata(conn, peerExtID, metadataSize)
	if err != nil {
		return nil, fmt.Errorf("download metadata: %w", err)
	}

	// Verify SHA1 of downloaded bytes matches the infohash.
	sum := sha1.Sum(raw)
	if sum != infohash {
		return nil, errors.New("metadata SHA1 mismatch")
	}

	return parseInfo(raw, infohash)
}

// ---- BitTorrent handshake (BEP 3) ----

var protoHeader = []byte("\x13BitTorrent protocol")

// extendedBit signals support for the BEP10 extension protocol.
// Reserved bytes: index 5, bit 4 (0x10).
var reservedBytes = [8]byte{0, 0, 0, 0, 0, 0x10, 0, 0}

func sendHandshake(w io.Writer, infohash [20]byte) error {
	var peerID [20]byte
	copy(peerID[:], "-GT0001-")
	// Fill the remaining 12 bytes with zeros for determinism.

	buf := make([]byte, 0, 68)
	buf = append(buf, protoHeader...)
	buf = append(buf, reservedBytes[:]...)
	buf = append(buf, infohash[:]...)
	buf = append(buf, peerID[:]...)

	_, err := w.Write(buf)
	return err
}

func recvHandshake(r io.Reader, wantHash [20]byte) ([20]byte, [8]byte, error) {
	// pstrlen + pstr
	header := make([]byte, 20)
	if _, err := io.ReadFull(r, header); err != nil {
		return [20]byte{}, [8]byte{}, err
	}
	if header[0] != 19 || string(header[1:]) != "BitTorrent protocol" {
		return [20]byte{}, [8]byte{}, errors.New("not a BitTorrent peer")
	}

	var extBits [8]byte
	if _, err := io.ReadFull(r, extBits[:]); err != nil {
		return [20]byte{}, [8]byte{}, err
	}

	var gotHash [20]byte
	if _, err := io.ReadFull(r, gotHash[:]); err != nil {
		return [20]byte{}, [8]byte{}, err
	}
	if gotHash != wantHash {
		return [20]byte{}, [8]byte{}, errors.New("infohash mismatch in handshake")
	}

	var peerID [20]byte
	if _, err := io.ReadFull(r, peerID[:]); err != nil {
		return [20]byte{}, [8]byte{}, err
	}
	return peerID, extBits, nil
}

// ---- BEP10 extension protocol ----

func sendExtMessage(w io.Writer, extID byte, payload []byte) error {
	// Wire format: [4-byte length][1-byte msg id = 20][1-byte ext id][payload]
	msgLen := uint32(2 + len(payload))
	buf := make([]byte, 4+int(msgLen))
	binary.BigEndian.PutUint32(buf[:4], msgLen)
	buf[4] = 20 // BEP10 extended message type
	buf[5] = extID
	copy(buf[6:], payload)
	_, err := w.Write(buf)
	return err
}

func readMessage(r io.Reader) (id byte, payload []byte, err error) {
	var lenBuf [4]byte
	if _, err = io.ReadFull(r, lenBuf[:]); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		// Keep-alive
		return readMessage(r)
	}
	if length > 1<<20 { // 1 MiB sanity limit per message
		return 0, nil, fmt.Errorf("message too large: %d bytes", length)
	}
	msg := make([]byte, length)
	if _, err = io.ReadFull(r, msg); err != nil {
		return
	}
	id = msg[0]
	payload = msg[1:]
	return
}

// exchangeExtHandshake sends our BEP10 handshake and reads the peer's.
// Returns the peer's metadata_size and its ut_metadata extension ID.
func exchangeExtHandshake(rw io.ReadWriter) (metadataSize int, peerExtID byte, err error) {
	handshake := map[string]interface{}{
		"m": map[string]interface{}{
			"ut_metadata": int64(1),
		},
	}
	payload, err := bencode.Encode(handshake)
	if err != nil {
		return
	}
	if err = sendExtMessage(rw, 0, payload); err != nil {
		return
	}

	// Read messages until we get the ext handshake (ext id 0).
	for {
		id, data, e := readMessage(rw)
		if e != nil {
			err = e
			return
		}
		if id != 20 {
			continue
		}
		if len(data) < 1 {
			continue
		}
		if data[0] != 0 { // not handshake
			continue
		}
		d, e := bencode.DecodeDict(data[1:])
		if e != nil {
			err = e
			return
		}
		if sz, ok := bencode.GetInt64(d, "metadata_size"); ok {
			metadataSize = int(sz)
		}
		if m, ok := bencode.GetDict(d, "m"); ok {
			if extID, ok := bencode.GetInt64(m, "ut_metadata"); ok {
				peerExtID = byte(extID)
			}
		}
		return
	}
}

// downloadMetadata downloads all metadata pieces from the peer.
func downloadMetadata(rw io.ReadWriter, extID byte, metadataSize int) ([]byte, error) {
	numPieces := int(math.Ceil(float64(metadataSize) / float64(metadataPieceLen)))
	metadata := make([]byte, metadataSize)
	received := make([]bool, numPieces)

	// Request all pieces.
	for i := 0; i < numPieces; i++ {
		req := map[string]interface{}{
			"msg_type": int64(0), // request
			"piece":    int64(i),
		}
		payload, err := bencode.Encode(req)
		if err != nil {
			return nil, err
		}
		if err := sendExtMessage(rw, extID, payload); err != nil {
			return nil, err
		}
	}

	// Receive pieces.
	for {
		allReceived := true
		for _, r := range received {
			if !r {
				allReceived = false
				break
			}
		}
		if allReceived {
			break
		}

		msgID, data, err := readMessage(rw)
		if err != nil {
			return nil, err
		}
		if msgID != 20 {
			continue
		}
		if len(data) < 1 || data[0] != extID {
			continue
		}

		// data[1:] contains: bencoded header + raw piece data
		header, err := bencode.DecodeDict(data[1:])
		if err != nil {
			continue
		}
		msgType, _ := bencode.GetInt64(header, "msg_type")
		piece, _ := bencode.GetInt64(header, "piece")

		if msgType != 1 { // not a "data" message
			continue
		}
		if piece < 0 || int(piece) >= numPieces {
			continue
		}
		if received[piece] {
			continue
		}

		// The raw piece data comes after the bencoded header.
		// We need to find where the bencode ends and raw data begins.
		headerBytes, err := bencode.Encode(header)
		if err != nil {
			continue
		}
		headerLen := len(headerBytes)
		if 1+headerLen > len(data) {
			continue
		}
		pieceData := data[1+headerLen:]

		start := int(piece) * metadataPieceLen
		end := start + len(pieceData)
		if end > metadataSize {
			end = metadataSize
		}
		copy(metadata[start:end], pieceData)
		received[piece] = true
	}
	return metadata, nil
}

// ---- info dictionary parsing ----

func parseInfo(raw []byte, infohash [20]byte) (*TorrentInfo, error) {
	d, err := bencode.DecodeDict(raw)
	if err != nil {
		return nil, fmt.Errorf("parse info dict: %w", err)
	}

	info := &TorrentInfo{InfoHash: infohash}

	name, _ := bencode.GetString(d, "name")
	if name == "" {
		name, _ = bencode.GetString(d, "name.utf-8")
	}
	info.Name = name

	// Single-file mode.
	if length, ok := bencode.GetInt64(d, "length"); ok {
		info.Length = length
		return info, nil
	}

	// Multi-file mode.
	files, ok := bencode.GetList(d, "files")
	if !ok {
		return nil, errors.New("info dict has neither 'length' nor 'files'")
	}
	for _, f := range files {
		fd, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		length, _ := bencode.GetInt64(fd, "length")
		var filePath string
		if pathList, ok := bencode.GetList(fd, "path.utf-8"); ok && len(pathList) > 0 {
			filePath = joinPath(pathList)
		} else if pathList, ok := bencode.GetList(fd, "path"); ok && len(pathList) > 0 {
			filePath = joinPath(pathList)
		}
		info.Files = append(info.Files, FileInfo{Path: filePath, Length: length})
	}
	return info, nil
}

func joinPath(parts []interface{}) string {
	result := ""
	for i, p := range parts {
		if b, ok := p.([]byte); ok {
			if i > 0 {
				result += "/"
			}
			result += string(b)
		}
	}
	return result
}
