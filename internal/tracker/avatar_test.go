package tracker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/config"
	"github.com/roiarthurb/xmpp-releasetracker/internal/store"
)

// avatarServer serves *image as a PNG at /owner.png and counts requests.
func avatarServer(t *testing.T, image *string, contentType string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(*image))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newAvatarTracker(t *testing.T) (*Tracker, *fakeMatrix) {
	t.Helper()
	tr, _ := newTestTracker(t)
	m := &fakeMatrix{}
	tr.matrix = m
	return tr, m
}

func TestMatrixAvatarUploadsOnceAndCaches(t *testing.T) {
	tr, m := newAvatarTracker(t)
	image := "PNG1"
	srv, hits := avatarServer(t, &image, "image/png")
	u := srv.URL + "/owner.png"

	if got := tr.matrixAvatar(u); got != "mxc://example.org/upload1" {
		t.Fatalf("first matrixAvatar = %q", got)
	}
	if got := tr.matrixAvatar(u); got != "mxc://example.org/upload1" {
		t.Fatalf("cached matrixAvatar = %q", got)
	}
	if m.uploads != 1 || hits.Load() != 1 {
		t.Errorf("got %d uploads / %d fetches, want 1 / 1 (second call served from cache)", m.uploads, hits.Load())
	}
}

func TestMatrixAvatarRefreshReuploadsOnlyOnChange(t *testing.T) {
	tr, m := newAvatarTracker(t)
	image := "PNG1"
	srv, hits := avatarServer(t, &image, "image/png")
	u := srv.URL + "/owner.png"
	expire := func() {
		c, _ := tr.store.GetMedia(u)
		c.CheckedAt = time.Now().Add(-2 * avatarRefresh)
		if err := tr.store.PutMedia(u, *c); err != nil {
			t.Fatal(err)
		}
	}

	tr.matrixAvatar(u)
	expire()
	if got := tr.matrixAvatar(u); got != "mxc://example.org/upload1" || m.uploads != 1 {
		t.Errorf("unchanged image: got %q after %d uploads, want upload1 after 1", got, m.uploads)
	}

	image = "PNG2"
	expire()
	if got := tr.matrixAvatar(u); got != "mxc://example.org/upload2" || m.uploads != 2 {
		t.Errorf("changed image: got %q after %d uploads, want upload2 after 2", got, m.uploads)
	}
	if hits.Load() != 3 {
		t.Errorf("fetched %d times, want 3", hits.Load())
	}
}

func TestMatrixAvatarFallsBackToStaleUpload(t *testing.T) {
	tr, m := newAvatarTracker(t)
	u := "http://127.0.0.1:1/owner.png" // unreachable
	if err := tr.store.PutMedia(u, store.Media{MXCURL: "mxc://example.org/old", SHA256: "x", CheckedAt: time.Now().Add(-2 * avatarRefresh)}); err != nil {
		t.Fatal(err)
	}
	if got := tr.matrixAvatar(u); got != "mxc://example.org/old" {
		t.Errorf("matrixAvatar = %q, want the stale upload", got)
	}

	// Upload failure with nothing cached: no avatar.
	image := "PNG"
	srv, _ := avatarServer(t, &image, "image/png")
	m.uploadErr = errors.New("boom")
	if got := tr.matrixAvatar(srv.URL + "/new.png"); got != "" {
		t.Errorf("matrixAvatar after failed upload = %q, want empty", got)
	}
}

func TestMatrixAvatarRejectsNonImage(t *testing.T) {
	tr, m := newAvatarTracker(t)
	page := "<html>login</html>"
	srv, _ := avatarServer(t, &page, "text/html; charset=utf-8")
	if got := tr.matrixAvatar(srv.URL + "/owner.png"); got != "" || m.uploads != 0 {
		t.Errorf("non-image: got %q after %d uploads, want nothing uploaded", got, m.uploads)
	}
}

func TestMatrixAvatarWithoutMatrixOrURL(t *testing.T) {
	tr, _ := newTestTracker(t) // no Matrix client
	if got := tr.matrixAvatar("https://example.org/a.png"); got != "" {
		t.Errorf("without Matrix: %q", got)
	}
	tr2, _ := newAvatarTracker(t)
	if got := tr2.matrixAvatar(""); got != "" {
		t.Errorf("empty URL: %q", got)
	}
}

func TestMatrixNotificationCarriesAvatar(t *testing.T) {
	tr, m := newAvatarTracker(t)
	image := "PNG"
	srv, _ := avatarServer(t, &image, "image/png")
	r := release("v1.0.0", time.Hour)
	r.AvatarURL = srv.URL + "/owner.png"

	for _, target := range []string{"@alice:example.org", "@bob:example.org"} {
		if err := tr.sendNotification(matrixDirect(target), "github", r); err != nil {
			t.Fatalf("sendNotification: %v", err)
		}
	}
	if len(m.direct) != 2 || m.uploads != 1 {
		t.Fatalf("got %d messages / %d uploads, want 2 / 1", len(m.direct), m.uploads)
	}
	for _, msg := range m.direct {
		if want := `<img src="mxc://example.org/upload1"`; len(msg.html) < len(want) || msg.html[:len(want)] != want {
			t.Errorf("message HTML does not start with the avatar: %q", msg.html)
		}
	}
}

func matrixDirect(userID string) config.NotifyTarget {
	return config.NotifyTarget{ID: userID, Type: config.NotifyMatrixDirect}
}

func TestSizedAvatarURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/owner.png":                          "https://github.com/owner.png?size=64",
		"https://avatars.githubusercontent.com/u/1?v=4":         "https://avatars.githubusercontent.com/u/1?size=64&v=4",
		"https://codeberg.org/avatars/6cd643d25089a2c02094173e": "https://codeberg.org/avatars/6cd643d25089a2c02094173e",
	} {
		if got := sizedAvatarURL(in); got != want {
			t.Errorf("sizedAvatarURL(%q) = %q, want %q", in, got, want)
		}
	}
}
