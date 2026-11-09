package store

import (
	"database/sql"
	"errors"
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
);
CREATE TABLE IF NOT EXISTS media (
    original_url TEXT PRIMARY KEY,
    mxc_url      TEXT NOT NULL,
    sha256       TEXT NOT NULL,
    checked_at   DATETIME NOT NULL
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

// IsFirstSeen returns true if no releases have been recorded yet for this repo.
// Used to suppress announcements on the initial discovery of a repo.
func (s *Store) IsFirstSeen(backend, repoSlug string) (bool, error) {
	var count int
	err := s.db.Get(&count,
		`SELECT COUNT(*) FROM seen_releases WHERE backend=? AND repo_slug=?`,
		backend, repoSlug)
	if err != nil {
		return false, fmt.Errorf("querying seen_releases: %w", err)
	}
	return count == 0, nil
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

// PruneKeepNewest deletes all but the newest keep releases per repository,
// bounding database growth for long-running deployments. keep must
// comfortably exceed the number of releases fetched per poll (releasesLimit)
// so a pruned release can never reappear in a feed and be re-announced.
// Rows without a timestamp sort last and are pruned first.
func (s *Store) PruneKeepNewest(keep int) (int64, error) {
	res, err := s.db.Exec(`
DELETE FROM seen_releases WHERE rowid IN (
    SELECT rowid FROM (
        SELECT rowid,
               ROW_NUMBER() OVER (
                   PARTITION BY backend, repo_slug
                   ORDER BY published_at DESC
               ) AS rn
        FROM seen_releases
    ) WHERE rn > ?
)`, keep)
	if err != nil {
		return 0, fmt.Errorf("pruning seen_releases: %w", err)
	}
	return res.RowsAffected()
}

// Media is a cached upload of a remote image to the Matrix media repository.
type Media struct {
	MXCURL    string    `db:"mxc_url"`
	SHA256    string    `db:"sha256"`
	CheckedAt time.Time `db:"checked_at"`
}

// GetMedia returns the cached upload for originalURL, or nil when there is none.
func (s *Store) GetMedia(originalURL string) (*Media, error) {
	var m Media
	err := s.db.Get(&m, `SELECT mxc_url, sha256, checked_at FROM media WHERE original_url=?`, originalURL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying media: %w", err)
	}
	return &m, nil
}

// PutMedia records (or replaces) the cached upload for originalURL.
func (s *Store) PutMedia(originalURL string, m Media) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO media (original_url, mxc_url, sha256, checked_at) VALUES (?, ?, ?, ?)`,
		originalURL, m.MXCURL, m.SHA256, m.CheckedAt)
	if err != nil {
		return fmt.Errorf("storing media: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
