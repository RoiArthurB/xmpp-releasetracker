package store

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS seen_releases (
    backend      TEXT NOT NULL,
    repo_slug    TEXT NOT NULL,
    tag_name     TEXT NOT NULL,
    published_at DATETIME,
    PRIMARY KEY (backend, repo_slug, tag_name)
);`

type Store struct {
	db *sqlx.DB
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

// HasSeen returns true if this release tag has already been recorded for the repo.
func (s *Store) HasSeen(backend, repoSlug, tagName string) (bool, error) {
	var count int
	err := s.db.Get(&count,
		`SELECT COUNT(*) FROM seen_releases WHERE backend=? AND repo_slug=? AND tag_name=?`,
		backend, repoSlug, tagName)
	if err != nil {
		return false, fmt.Errorf("querying seen_releases: %w", err)
	}
	return count > 0, nil
}

// MarkSeen records a release so it won't be announced again.
func (s *Store) MarkSeen(backend, repoSlug, tagName string, publishedAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO seen_releases (backend, repo_slug, tag_name, published_at) VALUES (?, ?, ?, ?)`,
		backend, repoSlug, tagName, publishedAt)
	if err != nil {
		return fmt.Errorf("inserting seen_releases: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
