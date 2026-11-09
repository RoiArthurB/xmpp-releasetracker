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

func TestMergeNotifyMatrixTargets(t *testing.T) {
	defaults := []config.NotifyTarget{{ID: "#r:example.org", Type: config.NotifyMatrixRoom}}
	extras := []config.NotifyTarget{
		{ID: "#r:example.org", Type: config.NotifyMatrixRoom},   // duplicate, dropped
		{ID: "#r2:example.org", Type: config.NotifyMatrixRoom},  // distinct ID, kept
		{ID: "@a:example.org", Type: config.NotifyMatrixDirect}, // kept
	}
	if got := mergeNotify(defaults, extras); len(got) != 3 {
		t.Errorf("mergeNotify = %+v, want 3 distinct targets", got)
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

func TestFormatReleaseMatrix(t *testing.T) {
	r := backend.Release{
		RepoSlug:  "owner/repo",
		TagName:   "v1.0.0",
		Name:      "First <release>",
		URL:       "https://github.com/owner/repo/releases/tag/v1.0.0?a=1&b=2",
		Body:      "line one\n<script>alert(1)</script>",
		AvatarURL: "https://github.com/owner.png",
	}
	plain, html := formatReleaseMatrix("github", r, "")

	wantPlain := "[Github] owner/repo — v1.0.0 “First <release>”\n" + r.URL + "\n\nline one\n<script>alert(1)</script>"
	if plain != wantPlain {
		t.Errorf("plain = %q, want %q", plain, wantPlain)
	}
	if strings.Contains(plain, r.AvatarURL) {
		t.Error("avatar URL leaked into the Matrix body")
	}
	wantHTML := "<strong>[Github] owner/repo — v1.0.0 “First &lt;release&gt;”</strong><br>" +
		`<a href="https://github.com/owner/repo/releases/tag/v1.0.0?a=1&amp;b=2">https://github.com/owner/repo/releases/tag/v1.0.0?a=1&amp;b=2</a>` +
		"<br><br>line one<br>&lt;script&gt;alert(1)&lt;/script&gt;"
	if html != wantHTML {
		t.Errorf("html = %q\nwant   %q", html, wantHTML)
	}
}

func TestFormatReleaseMatrixWithAvatar(t *testing.T) {
	r := backend.Release{RepoSlug: "owner/repo", TagName: "v1.0.0", URL: "https://example.org/r"}
	plain, html := formatReleaseMatrix("github", r, "mxc://example.org/abc")
	if !strings.HasPrefix(html, `<img src="mxc://example.org/abc" alt="avatar" width="32" height="32"> <strong>`) {
		t.Errorf("avatar not rendered before headline: %q", html)
	}
	if strings.Contains(plain, "mxc://") {
		t.Errorf("avatar leaked into plain body: %q", plain)
	}

	// Anything that is not an mxc:// URI would not render; never embed it.
	_, html = formatReleaseMatrix("github", r, "https://example.org/a.png")
	if strings.Contains(html, "<img") {
		t.Errorf("non-mxc avatar embedded: %q", html)
	}
}
