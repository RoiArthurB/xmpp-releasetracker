package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSeenLifecycle(t *testing.T) {
	s := openTestStore(t)

	first, err := s.IsFirstSeen("github", "owner/repo")
	if err != nil || !first {
		t.Fatalf("IsFirstSeen on empty store = %v, %v; want true, nil", first, err)
	}

	if err := s.MarkSeen("github", "owner/repo", "v1.0.0", time.Now()); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	seen, err := s.HasSeen("github", "owner/repo", "v1.0.0")
	if err != nil || !seen {
		t.Fatalf("HasSeen after MarkSeen = %v, %v; want true, nil", seen, err)
	}
	first, err = s.IsFirstSeen("github", "owner/repo")
	if err != nil || first {
		t.Fatalf("IsFirstSeen after MarkSeen = %v, %v; want false, nil", first, err)
	}

	// Same tag on a different backend or repo must not count as seen.
	if seen, _ := s.HasSeen("gitlab", "owner/repo", "v1.0.0"); seen {
		t.Error("HasSeen leaked across backends")
	}
	if seen, _ := s.HasSeen("github", "other/repo", "v1.0.0"); seen {
		t.Error("HasSeen leaked across repos")
	}
}

func TestPruneKeepNewest(t *testing.T) {
	s := openTestStore(t)

	base := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < 60; i++ {
		tag := fmt.Sprintf("v%d", i)
		if err := s.MarkSeen("github", "owner/repo", tag, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("MarkSeen %s: %v", tag, err)
		}
	}
	// A second repo under the keep limit must be untouched.
	if err := s.MarkSeen("github", "other/repo", "v1", time.Now()); err != nil {
		t.Fatalf("MarkSeen other/repo: %v", err)
	}

	n, err := s.PruneKeepNewest(50)
	if err != nil {
		t.Fatalf("PruneKeepNewest: %v", err)
	}
	if n != 10 {
		t.Errorf("pruned %d rows, want 10", n)
	}

	// The 10 oldest are gone, the newest 50 remain.
	for i := 0; i < 10; i++ {
		if seen, _ := s.HasSeen("github", "owner/repo", fmt.Sprintf("v%d", i)); seen {
			t.Errorf("v%d should have been pruned", i)
		}
	}
	for _, i := range []int{10, 35, 59} {
		if seen, _ := s.HasSeen("github", "owner/repo", fmt.Sprintf("v%d", i)); !seen {
			t.Errorf("v%d should have been kept", i)
		}
	}
	if seen, _ := s.HasSeen("github", "other/repo", "v1"); !seen {
		t.Error("other/repo v1 should have been kept")
	}
}

func TestMediaCache(t *testing.T) {
	s := openTestStore(t)

	m, err := s.GetMedia("https://example.org/a.png")
	if err != nil || m != nil {
		t.Fatalf("GetMedia on empty store = %+v, %v; want nil, nil", m, err)
	}

	checked := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	if err := s.PutMedia("https://example.org/a.png", Media{MXCURL: "mxc://hs/1", SHA256: "aa", CheckedAt: checked}); err != nil {
		t.Fatalf("PutMedia: %v", err)
	}
	if err := s.PutMedia("https://example.org/a.png", Media{MXCURL: "mxc://hs/2", SHA256: "bb", CheckedAt: checked}); err != nil {
		t.Fatalf("PutMedia (replace): %v", err)
	}
	m, err = s.GetMedia("https://example.org/a.png")
	if err != nil || m == nil {
		t.Fatalf("GetMedia = %+v, %v", m, err)
	}
	if m.MXCURL != "mxc://hs/2" || m.SHA256 != "bb" || !m.CheckedAt.Equal(checked) {
		t.Errorf("GetMedia = %+v, want mxc://hs/2, bb, %v", m, checked)
	}
}
