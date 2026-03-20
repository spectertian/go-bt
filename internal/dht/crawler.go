// Package dht implements a DHT (Distributed Hash Table) spy crawler based on
// BEP 5 (http://www.bittorrent.org/beps/bep_0005.html).
//
// The crawler joins the DHT network and listens for announce_peer messages,
// capturing infohashes as peers announce torrents they are downloading.
package dht

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"net"
	"time"

	"github.com/spectertian/go-bt/internal/bencode"
)

// bootstrapNodes are the well-known DHT bootstrap nodes.
var bootstrapNodes = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
	"dht.aelitis.com:6881",
}

// Hit represents a discovered infohash and the peer TCP address to fetch
// metadata from.
type Hit struct {
	InfoHash [20]byte
	PeerAddr string // "ip:port" TCP address
}

// Crawler is a DHT spy node that discovers infohashes by joining the DHT
// network and listening for announce_peer messages.
type Crawler struct {
	nodeID [20]byte
	conn   *net.UDPConn
	hits   chan<- Hit
	nodes  chan *net.UDPAddr
}

// NewCrawler creates a new DHT crawler that will send discovered hits
// to the given channel.
func NewCrawler(hits chan<- Hit) *Crawler {
	var id [20]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic("dht: failed to generate node id: " + err.Error())
	}
	return &Crawler{
		nodeID: id,
		hits:   hits,
		nodes:  make(chan *net.UDPAddr, 2000),
	}
}

// Run starts the DHT crawler and blocks until ctx is cancelled.
func (c *Crawler) Run(ctx context.Context) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		log.Printf("dht: failed to listen: %v", err)
		return
	}
	c.conn = conn
	defer conn.Close()

	log.Printf("dht: crawler started on %s with node id %x", conn.LocalAddr(), c.nodeID)

	// Bootstrap: resolve and enqueue the well-known nodes.
	for _, addr := range bootstrapNodes {
		if udpAddr, err := net.ResolveUDPAddr("udp4", addr); err == nil {
			c.nodes <- udpAddr
		}
	}

	// Worker: continuously send find_node to queued nodes.
	go c.sendLoop(ctx)

	// Read loop.
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			log.Printf("dht: read error: %v", err)
			continue
		}
		c.handleMessage(buf[:n], from)
	}
}

// sendLoop continuously picks nodes from the queue and sends find_node.
func (c *Crawler) sendLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			select {
			case addr := <-c.nodes:
				c.sendFindNode(addr)
			default:
				// Queue empty – re-bootstrap.
				for _, addr := range bootstrapNodes {
					if udpAddr, err := net.ResolveUDPAddr("udp4", addr); err == nil {
						c.sendFindNode(udpAddr)
					}
				}
			}
		}
	}
}

// sendFindNode sends a find_node query with a random target ID.
func (c *Crawler) sendFindNode(addr *net.UDPAddr) {
	var target [20]byte
	if _, err := rand.Read(target[:]); err != nil {
		return
	}
	tid := randTID()
	msg := map[string]interface{}{
		"t": tid,
		"y": []byte("q"),
		"q": []byte("find_node"),
		"a": map[string]interface{}{
			"id":     c.nodeID[:],
			"target": target[:],
		},
	}
	c.send(msg, addr)
}

// handleMessage parses an incoming UDP KRPC message and acts on it.
func (c *Crawler) handleMessage(data []byte, from *net.UDPAddr) {
	d, err := bencode.DecodeDict(data)
	if err != nil {
		return
	}

	y, _ := bencode.GetString(d, "y")
	switch y {
	case "r":
		c.handleResponse(d)
	case "q":
		c.handleQuery(d, from)
	}
}

// handleResponse processes a KRPC response, extracting node addresses.
func (c *Crawler) handleResponse(d map[string]interface{}) {
	r, ok := bencode.GetDict(d, "r")
	if !ok {
		return
	}
	// Parse compact node info (26 bytes per node: 20 id + 4 ip + 2 port).
	nodesRaw, ok := bencode.GetBytes(r, "nodes")
	if !ok {
		return
	}
	for i := 0; i+26 <= len(nodesRaw); i += 26 {
		ip := net.IP(nodesRaw[i+20 : i+24])
		port := binary.BigEndian.Uint16(nodesRaw[i+24 : i+26])
		addr := &net.UDPAddr{IP: ip, Port: int(port)}
		select {
		case c.nodes <- addr:
		default:
		}
	}
}

// handleQuery processes an incoming KRPC query and responds.
func (c *Crawler) handleQuery(d map[string]interface{}, from *net.UDPAddr) {
	q, _ := bencode.GetString(d, "q")
	tid, _ := bencode.GetBytes(d, "t")

	switch q {
	case "ping":
		c.sendPong(tid, from)

	case "find_node":
		c.sendNodesReply(tid, from)

	case "get_peers":
		a, ok := bencode.GetDict(d, "a")
		if !ok {
			return
		}
		ih, ok := bencode.GetBytes(a, "info_hash")
		if ok && len(ih) == 20 {
			var infohash [20]byte
			copy(infohash[:], ih)
			select {
			case c.hits <- Hit{InfoHash: infohash, PeerAddr: from.String()}:
			default:
			}
		}
		// Respond with token + fake nodes so the remote node doesn't ignore us.
		c.sendGetPeersReply(tid, from, ih)

	case "announce_peer":
		a, ok := bencode.GetDict(d, "a")
		if !ok {
			return
		}
		ih, ok := bencode.GetBytes(a, "info_hash")
		if ok && len(ih) == 20 {
			var infohash [20]byte
			copy(infohash[:], ih)

			// Determine the TCP port for metadata fetching.
			// BEP 5: if implied_port == 1, use the source UDP port; otherwise
			// use the port field from the message.
			port := from.Port
			if implied, ok := bencode.GetInt64(a, "implied_port"); !ok || implied == 0 {
				if p, ok := bencode.GetInt64(a, "port"); ok && p > 0 {
					port = int(p)
				}
			}
			peerAddr := fmt.Sprintf("%s:%d", from.IP.String(), port)
			select {
			case c.hits <- Hit{InfoHash: infohash, PeerAddr: peerAddr}:
			default:
			}
		}
		// Acknowledge.
		c.sendPong(tid, from)
	}
}

func (c *Crawler) sendPong(tid []byte, to *net.UDPAddr) {
	msg := map[string]interface{}{
		"t": tid,
		"y": []byte("r"),
		"r": map[string]interface{}{
			"id": c.nodeID[:],
		},
	}
	c.send(msg, to)
}

func (c *Crawler) sendNodesReply(tid []byte, to *net.UDPAddr) {
	msg := map[string]interface{}{
		"t": tid,
		"y": []byte("r"),
		"r": map[string]interface{}{
			"id":    c.nodeID[:],
			"nodes": []byte{},
		},
	}
	c.send(msg, to)
}

func (c *Crawler) sendGetPeersReply(tid []byte, to *net.UDPAddr, infoHash []byte) {
	// Use a simple token derived from the remote IP.
	token := to.IP.To4()
	if token == nil {
		token = []byte{0, 0, 0, 0}
	}
	msg := map[string]interface{}{
		"t": tid,
		"y": []byte("r"),
		"r": map[string]interface{}{
			"id":    neighborID(infoHash, c.nodeID),
			"token": token,
			"nodes": []byte{},
		},
	}
	c.send(msg, to)
}

func (c *Crawler) send(msg map[string]interface{}, to *net.UDPAddr) {
	data, err := bencode.Encode(msg)
	if err != nil {
		return
	}
	c.conn.WriteToUDP(data, to)
}

// neighborID returns a node ID that shares the first 10 bytes with target,
// which makes us appear to be a "close" node in DHT routing tables.
func neighborID(target []byte, self [20]byte) []byte {
	id := make([]byte, 20)
	copy(id[:10], target[:10])
	copy(id[10:], self[10:])
	return id
}

// randTID generates a random 2-byte transaction ID.
func randTID() []byte {
	n, _ := rand.Int(rand.Reader, big.NewInt(65536))
	tid := make([]byte, 2)
	binary.BigEndian.PutUint16(tid, uint16(n.Int64()))
	return tid
}
