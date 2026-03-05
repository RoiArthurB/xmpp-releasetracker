package store

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS last_seen (
    backend      TEXT NOT NULL,
    repo_slug    TEXT NOT NULL,
    tag_name     TEXT NOT NULL,
    published_at DATETIME NOT NULL,
    PRIMARY KEY (backend, repo_slug)
);`

type Store struct {
	db *sqlx.DB
}

type LastSeen struct {
	Backend     string    `db:"backend"`
	RepoSlug    string    `db:"repo_slug"`
	TagName     string    `db:"tag_name"`
	PublishedAt time.Time `db:"published_at"`
}

func Open(path string) (*Store, error) {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) GetLastSeen(backend, repoSlug string) (*LastSeen, error) {
	var ls LastSeen
	err := s.db.Get(&ls, `SELECT backend, repo_slug, tag_name, published_at FROM last_seen WHERE backend=? AND repo_slug=?`, backend, repoSlug)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("querying last_seen: %w", err)
	}
	return &ls, nil
}

func (s *Store) SetLastSeen(backend, repoSlug, tagName string, publishedAt time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO last_seen (backend, repo_slug, tag_name, published_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(backend, repo_slug) DO UPDATE SET
			tag_name=excluded.tag_name,
			published_at=excluded.published_at`,
		backend, repoSlug, tagName, publishedAt)
	if err != nil {
		return fmt.Errorf("updating last_seen: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
