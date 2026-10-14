package main

import (
	"database/sql"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Store is the server database (one SQLite file in the data folder).
type Store struct {
	*sql.DB
	mu sync.Mutex // serializes writes (SQLite allows one writer)
}

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL, username TEXT NOT NULL UNIQUE COLLATE NOCASE, email TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
  password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', disabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')), last_login TEXT);
CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_type TEXT NOT NULL, device_id TEXT NOT NULL, device_name TEXT NOT NULL DEFAULT '', app TEXT NOT NULL DEFAULT '',
  token TEXT NOT NULL UNIQUE, ip TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')), last_seen TEXT NOT NULL DEFAULT (datetime('now')));
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id, device_type);
CREATE TABLE IF NOT EXISTS libraries (
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, kind TEXT NOT NULL, path TEXT NOT NULL,
  last_scan TEXT);
-- one table for everything in the libraries: kind = movie | series | season | episode | album | track
CREATE TABLE IF NOT EXISTS items (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  kind TEXT NOT NULL, parent_id INTEGER REFERENCES items(id) ON DELETE CASCADE,
  title TEXT NOT NULL, sort_title TEXT NOT NULL DEFAULT '', year TEXT NOT NULL DEFAULT '',
  overview TEXT NOT NULL DEFAULT '', genres TEXT NOT NULL DEFAULT '', rating TEXT NOT NULL DEFAULT '',
  runtime INTEGER NOT NULL DEFAULT 0, duration REAL NOT NULL DEFAULT 0,
  season INTEGER NOT NULL DEFAULT 0, episode INTEGER NOT NULL DEFAULT 0, track INTEGER NOT NULL DEFAULT 0,
  artist TEXT NOT NULL DEFAULT '', album TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL DEFAULT 0,
  video_codec TEXT NOT NULL DEFAULT '', audio_codec TEXT NOT NULL DEFAULT '', container TEXT NOT NULL DEFAULT '',
  poster TEXT NOT NULL DEFAULT '', backdrop TEXT NOT NULL DEFAULT '', subtitles TEXT NOT NULL DEFAULT '',
  key TEXT NOT NULL, added_at TEXT NOT NULL DEFAULT (datetime('now')), seen INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS idx_items_key ON items(library_id, key);
CREATE INDEX IF NOT EXISTS idx_items_kind ON items(kind, id);
CREATE INDEX IF NOT EXISTS idx_items_parent ON items(parent_id);
CREATE TABLE IF NOT EXISTS tv_links (
  code TEXT PRIMARY KEY, secret TEXT NOT NULL, device_id TEXT NOT NULL, device_name TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT '', user_id INTEGER, token TEXT, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS link_fails (user_id INTEGER NOT NULL, at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS progress (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, item_id INTEGER NOT NULL,
  position REAL NOT NULL, duration REAL NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY(user_id, item_id));
`

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(8000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Setting(key, def string) string {
	var v string
	if err := s.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}

func (s *Store) SettingBool(key string, def bool) bool {
	v := s.Setting(key, "")
	if v == "" {
		return def
	}
	return v == "1" || strings.EqualFold(v, "true")
}

func (s *Store) Exec2(q string, args ...any) (sql.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Exec(q, args...)
}

func (s *Store) Configured() bool {
	var n int
	_ = s.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n)
	return n > 0
}
