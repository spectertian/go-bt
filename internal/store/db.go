// Package store provides SQLite-backed persistence for torrent metadata.
// It uses FTS5 full-text search for fast keyword queries.
package store

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

// Torrent represents a stored torrent record.
type Torrent struct {
	InfoHash  string    `json:"infohash"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	FileCount int       `json:"file_count"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchResult wraps a page of search results.
type SearchResult struct {
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
	Items    []Torrent `json:"items"`
}

// DB holds the SQLite connection pool.
type DB struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at the given file path.
func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite allows only one writer at a time.

	store := &DB{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

// Close closes the underlying database connection.
func (s *DB) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS torrents (
    infohash   TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    file_count INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS torrents_fts USING fts5(
    name,
    content='torrents',
    content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS torrents_ai AFTER INSERT ON torrents BEGIN
    INSERT INTO torrents_fts(rowid, name) VALUES (new.rowid, new.name);
END;
CREATE TRIGGER IF NOT EXISTS torrents_au AFTER UPDATE ON torrents BEGIN
    INSERT INTO torrents_fts(torrents_fts, rowid, name) VALUES ('delete', old.rowid, old.name);
    INSERT INTO torrents_fts(rowid, name) VALUES (new.rowid, new.name);
END;
CREATE TRIGGER IF NOT EXISTS torrents_ad AFTER DELETE ON torrents BEGIN
    INSERT INTO torrents_fts(torrents_fts, rowid, name) VALUES ('delete', old.rowid, old.name);
END;
`

func (s *DB) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

// Save inserts a torrent record. Duplicate infohashes are silently ignored.
func (s *DB) Save(infohash [20]byte, name string, size int64, fileCount int) error {
	ih := hex.EncodeToString(infohash[:])
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO torrents (infohash, name, size, file_count, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		ih, name, size, fileCount, time.Now().Unix(),
	)
	return err
}

// Search performs a full-text search and returns a paginated result.
// page is 1-indexed.
func (s *DB) Search(query string, page, pageSize int) (*SearchResult, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int
	var rows *sql.Rows
	var err error

	if query == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM torrents`).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = s.db.Query(
			`SELECT infohash, name, size, file_count, created_at
			 FROM torrents
			 ORDER BY created_at DESC
			 LIMIT ? OFFSET ?`,
			pageSize, offset,
		)
	} else {
		err = s.db.QueryRow(
			`SELECT COUNT(*) FROM torrents
			 WHERE rowid IN (SELECT rowid FROM torrents_fts WHERE name MATCH ?)`,
			query,
		).Scan(&total)
		if err != nil {
			return nil, err
		}
		rows, err = s.db.Query(
			`SELECT infohash, name, size, file_count, created_at
			 FROM torrents
			 WHERE rowid IN (SELECT rowid FROM torrents_fts WHERE name MATCH ?)
			 ORDER BY created_at DESC
			 LIMIT ? OFFSET ?`,
			query, pageSize, offset,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Torrent
	for rows.Next() {
		var t Torrent
		var ts int64
		if err := rows.Scan(&t.InfoHash, &t.Name, &t.Size, &t.FileCount, &ts); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(ts, 0)
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &SearchResult{
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Items:    items,
	}, nil
}

// Get retrieves a single torrent by its hex infohash.
func (s *DB) Get(infohash string) (*Torrent, error) {
	var t Torrent
	var ts int64
	err := s.db.QueryRow(
		`SELECT infohash, name, size, file_count, created_at FROM torrents WHERE infohash = ?`,
		infohash,
	).Scan(&t.InfoHash, &t.Name, &t.Size, &t.FileCount, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(ts, 0)
	return &t, nil
}

// Count returns the total number of stored torrents.
func (s *DB) Count() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM torrents`).Scan(&n)
	return n, err
}
