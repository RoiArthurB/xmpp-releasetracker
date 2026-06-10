package tracker

import (
	"strings"
	"testing"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
	"github.com/roiarthurb/xmpp-releasetracker/internal/config"
)

func TestMergeNotify(t *testing.T) {
	defaults := []config.NotifyTarget{
		{JID: "room@muc.example.org", Type: "muc"},
	}
	extras := []config.NotifyTarget{
		{JID: "room@muc.example.org", Type: "muc"},    // duplicate, dropped
		{JID: "room@muc.example.org", Type: "direct"}, // same JID, new type, kept
		{JID: "user@example.org", Type: "direct"},
	}

	got := mergeNotify(defaults, extras)
	want := []config.NotifyTarget{
		{JID: "room@muc.example.org", Type: "muc"},
		{JID: "room@muc.example.org", Type: "direct"},
		{JID: "user@example.org", Type: "direct"},
	}
	if len(got) != len(want) {
		t.Fatalf("mergeNotify returned %d targets, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMergeNotifyDoesNotMutateDefaults(t *testing.T) {
	defaults := []config.NotifyTarget{{JID: "a@example.org", Type: "muc"}}
	mergeNotify(defaults, []config.NotifyTarget{{JID: "b@example.org", Type: "muc"}})
	if len(defaults) != 1 {
		t.Errorf("defaults slice was mutated: %+v", defaults)
	}
}

func TestTruncateBody(t *testing.T) {
	t.Run("short body unchanged", func(t *testing.T) {
		if got := truncateBody("hello\nworld"); got != "hello\nworld" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("line limit", func(t *testing.T) {
		body := strings.Repeat("line\n", maxBodyLines+5)
		got := truncateBody(body)
		lines := strings.Split(got, "\n")
		// maxBodyLines content lines plus the ellipsis line.
		if len(lines) != maxBodyLines+1 {
			t.Errorf("got %d lines, want %d", len(lines), maxBodyLines+1)
		}
		if lines[len(lines)-1] != "…" {
			t.Errorf("last line = %q, want ellipsis", lines[len(lines)-1])
		}
	})

	t.Run("char limit counts runes", func(t *testing.T) {
		body := strings.Repeat("é", maxBodyChars+10)
		got := truncateBody(body)
		if n := len([]rune(got)); n != maxBodyChars+1 { // content + ellipsis
			t.Errorf("got %d runes, want %d", n, maxBodyChars+1)
		}
		if !strings.HasSuffix(got, "…") {
			t.Error("truncated body should end with ellipsis")
		}
	})
}

func TestFormatRelease(t *testing.T) {
	r := backend.Release{
		RepoSlug:  "owner/repo",
		TagName:   "v1.0.0",
		Name:      "First release",
		URL:       "https://github.com/owner/repo/releases/tag/v1.0.0",
		Body:      "Notes",
		AvatarURL: "https://github.com/owner.png",
	}

	body, avatarURL := formatRelease("github", r)

	if avatarURL != r.AvatarURL {
		t.Errorf("avatarURL = %q, want %q", avatarURL, r.AvatarURL)
	}
	lines := strings.Split(body, "\n")
	// XEP-0385 requires the avatar URL to be the very first line (offset 0).
	if lines[0] != r.AvatarURL {
		t.Errorf("first line = %q, want avatar URL", lines[0])
	}
	if want := "*[Github] owner/repo — v1.0.0 “First release”*"; lines[1] != want {
		t.Errorf("headline = %q, want %q", lines[1], want)
	}
	if lines[2] != r.URL {
		t.Errorf("third line = %q, want release URL", lines[2])
	}
	if !strings.HasSuffix(body, "\n\nNotes") {
		t.Errorf("body should end with release notes, got %q", body)
	}
}

func TestFormatReleaseNoAvatarNoName(t *testing.T) {
	r := backend.Release{
		RepoSlug: "owner/repo",
		TagName:  "v2.0.0",
		Name:     "v2.0.0", // same as tag: no quoted name in the headline
		URL:      "https://example.org/r/v2.0.0",
	}

	body, avatarURL := formatRelease("gitea", r)

	if avatarURL != "" {
		t.Errorf("avatarURL = %q, want empty", avatarURL)
	}
	lines := strings.Split(body, "\n")
	if want := "*[Gitea] owner/repo — v2.0.0*"; lines[0] != want {
		t.Errorf("headline = %q, want %q", lines[0], want)
	}
	if lines[1] != r.URL {
		t.Errorf("second line = %q, want release URL", lines[1])
	}
}
