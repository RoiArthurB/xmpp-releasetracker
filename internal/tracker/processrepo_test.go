package tracker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
	"github.com/roiarthurb/xmpp-releasetracker/internal/config"
	"github.com/roiarthurb/xmpp-releasetracker/internal/store"
)

// fakeBackend serves a fixed release list, newest first like the real APIs.
type fakeBackend struct {
	releases []backend.Release
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	out := make([]backend.Release, len(f.releases))
	copy(out, f.releases) // processRepo reverses in place; don't leak that back
	return out, nil
}
func (f *fakeBackend) GetUserStarredRepos(string) ([]string, error) { return nil, nil }
func (f *fakeBackend) GetOrgRepos(string) ([]string, error)        { return nil, nil }
func (f *fakeBackend) GetGroupRepos(string) ([]string, error)      { return nil, nil }

type sentMessage struct {
	to, body string
}

// fakeNotifier records every message instead of sending it.
type fakeNotifier struct {
	muc, direct []sentMessage
}

func (f *fakeNotifier) SendMUC(roomJID, body, _ string) error {
	f.muc = append(f.muc, sentMessage{roomJID, body})
	return nil
}
func (f *fakeNotifier) SendDirect(jid, body, _ string) error {
	f.direct = append(f.direct, sentMessage{jid, body})
	return nil
}

func newTestTracker(t *testing.T) (*Tracker, *fakeNotifier) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	n := &fakeNotifier{}
	return New(&config.Config{}, nil, st, n, false), n
}

func release(tag string, age time.Duration) backend.Release {
	return backend.Release{
		RepoSlug:    "owner/repo",
		TagName:     tag,
		Name:        tag,
		PublishedAt: time.Now().Add(-age),
		URL:         "https://example.org/" + tag,
	}
}

var mucTarget = []config.NotifyTarget{{JID: "room@muc.example.org", Type: "muc"}}

func TestFirstRunSnapshotsWithoutAnnouncing(t *testing.T) {
	tr, n := newTestTracker(t)
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", time.Hour)}}

	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	if len(n.muc) != 0 {
		t.Fatalf("first run announced %d messages, want 0", len(n.muc))
	}

	// Second poll, same releases: still silent.
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	if len(n.muc) != 0 {
		t.Fatalf("re-announced an already-seen release: %+v", n.muc)
	}
}

func TestNewReleaseAnnouncedOnce(t *testing.T) {
	tr, n := newTestTracker(t)
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", 48 * time.Hour)}}

	// First run snapshots v1.0.0.
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	// v1.1.0 appears (newest first, like real APIs).
	b.releases = []backend.Release{release("v1.1.0", time.Hour), release("v1.0.0", 48 * time.Hour)}
	for i := 0; i < 2; i++ { // second iteration checks idempotence
		if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
			t.Fatalf("processRepo: %v", err)
		}
	}

	if len(n.muc) != 1 {
		t.Fatalf("got %d announcements, want exactly 1: %+v", len(n.muc), n.muc)
	}
	if got := n.muc[0]; got.to != "room@muc.example.org" || !strings.Contains(got.body, "v1.1.0") {
		t.Errorf("unexpected announcement: %+v", got)
	}
}

func TestMultipleNewReleasesAnnouncedOldestFirst(t *testing.T) {
	tr, n := newTestTracker(t)
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", 72 * time.Hour)}}
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	b.releases = []backend.Release{
		release("v1.2.0", 1 * time.Hour),
		release("v1.1.0", 24 * time.Hour),
		release("v1.0.0", 72 * time.Hour),
	}
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	if len(n.muc) != 2 {
		t.Fatalf("got %d announcements, want 2: %+v", len(n.muc), n.muc)
	}
	if !strings.Contains(n.muc[0].body, "v1.1.0") || !strings.Contains(n.muc[1].body, "v1.2.0") {
		t.Errorf("announcements not in chronological order: %+v", n.muc)
	}
}

func TestOldReleaseOutsideWindowNotAnnounced(t *testing.T) {
	tr, n := newTestTracker(t)
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", 72 * time.Hour)}}
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	// A release older than recentWindow shows up later (e.g. the bot was
	// down, or the DB lost the row): marked seen but not announced.
	b.releases = append([]backend.Release{release("v0.9.0", recentWindow + 24*time.Hour)}, b.releases...)
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	if len(n.muc) != 0 {
		t.Fatalf("announced a release outside the recency window: %+v", n.muc)
	}
}

func TestSkipPrereleases(t *testing.T) {
	tr, n := newTestTracker(t)
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", 48 * time.Hour)}}
	if err := tr.processRepo(b, "owner/repo", mucTarget, true); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	pre := release("v1.1.0-rc1", time.Hour)
	pre.IsPrerelease = true
	stable := release("v1.1.0", time.Hour)
	b.releases = append([]backend.Release{stable, pre}, b.releases...)

	if err := tr.processRepo(b, "owner/repo", mucTarget, true); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	if len(n.muc) != 1 || !strings.Contains(n.muc[0].body, "v1.1.0") || strings.Contains(n.muc[0].body, "rc1") {
		t.Fatalf("want only the stable release announced, got: %+v", n.muc)
	}
}

func TestZeroTimestampReleaseAnnouncedWhenNew(t *testing.T) {
	tr, n := newTestTracker(t)
	// Tag-only backends (e.g. Gitea tags) return no timestamps.
	tagRelease := func(tag string) backend.Release {
		return backend.Release{RepoSlug: "owner/repo", TagName: tag, Name: tag, URL: "https://example.org/" + tag}
	}
	b := &fakeBackend{releases: []backend.Release{tagRelease("v1.0.0")}}
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	if len(n.muc) != 0 {
		t.Fatalf("first run announced: %+v", n.muc)
	}

	b.releases = []backend.Release{tagRelease("v1.1.0"), tagRelease("v1.0.0")}
	if err := tr.processRepo(b, "owner/repo", mucTarget, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	// No timestamp means the recency window cannot apply: genuinely new
	// tags must still be announced.
	if len(n.muc) != 1 || !strings.Contains(n.muc[0].body, "v1.1.0") {
		t.Fatalf("want v1.1.0 announced despite zero timestamp, got: %+v", n.muc)
	}
}

func TestDirectAndMUCTargetsBothNotified(t *testing.T) {
	tr, n := newTestTracker(t)
	targets := []config.NotifyTarget{
		{JID: "room@muc.example.org", Type: "muc"},
		{JID: "user@example.org", Type: "direct"},
	}
	b := &fakeBackend{releases: []backend.Release{release("v1.0.0", 48 * time.Hour)}}
	if err := tr.processRepo(b, "owner/repo", targets, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}
	b.releases = append([]backend.Release{release("v1.1.0", time.Hour)}, b.releases...)
	if err := tr.processRepo(b, "owner/repo", targets, false); err != nil {
		t.Fatalf("processRepo: %v", err)
	}

	if len(n.muc) != 1 || len(n.direct) != 1 {
		t.Fatalf("got %d muc / %d direct messages, want 1 each", len(n.muc), len(n.direct))
	}
	if n.direct[0].to != "user@example.org" {
		t.Errorf("direct message went to %q", n.direct[0].to)
	}
}
