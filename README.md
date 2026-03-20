# go-bt — 磁力搜索 / Magnet Search Website

A DHT-based magnet-link search website written in pure Go.

## Architecture

```
go-bt/
├── main.go                   # Entry point – wires DHT + metadata + web
├── internal/
│   ├── bencode/              # BEP 3 bencode encoder/decoder
│   ├── dht/                  # DHT crawler (BEP 5 – spy node)
│   ├── metadata/             # Torrent metadata fetcher (BEP 9 / BEP 10)
│   ├── store/                # SQLite storage with FTS5 full-text search
│   └── web/                  # HTTP server & embedded HTML templates
└── cmd/seed/                 # Dev helper: seed sample records
```

## How it works

1. **DHT Crawler** joins the BitTorrent DHT network as a spy node.  
   It listens for `announce_peer` messages and extracts the infohash +
   peer address from each one.

2. **Metadata Fetcher** connects to each announcing peer over TCP,
   performs a BitTorrent handshake with BEP 10 extensions enabled, and
   downloads the torrent's info dictionary via the `ut_metadata`
   BEP 9 extension.  The SHA‑1 of the downloaded bytes is verified
   against the infohash before the record is accepted.

3. **SQLite Storage** persists torrent records.  A FTS5 virtual table
   provides fast full‑text search over torrent names.

4. **Web Interface** exposes three pages:
   - `/` – home / hero search page
   - `/search?q=<query>&page=<n>` – paginated search results
     (also supports `?format=json` for an API response)
   - `/detail/<infohash>` – torrent detail with one‑click magnet link

## Usage

```bash
# Build
go build -o go-bt .

# Run (crawls DHT in background, serves UI on :8080)
./go-bt -addr :8080 -db torrents.db -workers 10
```

Then open <http://localhost:8080> in your browser.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | HTTP listen address |
| `-db` | `torrents.db` | SQLite database file |
| `-workers` | `10` | Number of parallel metadata‑fetch goroutines |

## Screenshots

### Home page
![Home page](https://github.com/user-attachments/assets/5cf69957-f776-466a-8929-cdcc116b9dc9)

### Torrent detail page
![Detail page](https://github.com/user-attachments/assets/d333b989-a718-431b-a73f-4f83db7186ee)

## Running tests

```bash
go test ./...
```
