package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/store"
)

const (
	// avatarRefresh is how long a cached upload is trusted before the source
	// image is fetched again to check whether it changed.
	avatarRefresh = 48 * time.Hour
	// maxAvatarBytes caps the avatar download; forge avatars are a few KiB.
	maxAvatarBytes = 1 << 20
)

// matrixAvatar returns an mxc:// URI for the image at avatarURL, or "" when it
// is unavailable. Matrix clients only render <img> tags whose source is an
// mxc:// URI, so remote avatars must be uploaded to the homeserver first.
// Uploads are cached in the store by source URL; after avatarRefresh the
// source is fetched again and re-uploaded only if its content changed. On any
// error the last known upload, if one exists, is still used.
func (t *Tracker) matrixAvatar(avatarURL string) string {
	if avatarURL == "" || t.matrix == nil {
		return ""
	}
	cached, err := t.store.GetMedia(avatarURL)
	if err != nil {
		log.Printf("Avatar cache lookup for %s: %v", avatarURL, err)
	}
	if cached != nil && time.Since(cached.CheckedAt) < avatarRefresh {
		return cached.MXCURL
	}
	stale := ""
	if cached != nil {
		stale = cached.MXCURL
	}

	data, contentType, err := fetchImage(t.http, sizedAvatarURL(avatarURL))
	if err != nil {
		log.Printf("Fetching avatar %s: %v", avatarURL, err)
		return stale
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	mxc := stale
	if cached == nil || cached.SHA256 != digest {
		if mxc, err = t.matrix.UploadMedia(data, contentType, imageFilename(avatarURL)); err != nil {
			log.Printf("Uploading avatar %s to Matrix: %v", avatarURL, err)
			return stale
		}
	}
	if err := t.store.PutMedia(avatarURL, store.Media{MXCURL: mxc, SHA256: digest, CheckedAt: time.Now()}); err != nil {
		log.Printf("Caching avatar %s: %v", avatarURL, err)
	}
	return mxc
}

// fetchImage downloads an image, rejecting non-image responses and anything
// larger than maxAvatarBytes.
func fetchImage(hc *http.Client, u string) ([]byte, string, error) {
	resp, err := hc.Get(u)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(contentType); err != nil || !strings.HasPrefix(mt, "image/") {
		return nil, "", fmt.Errorf("not an image (Content-Type %q)", contentType)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("empty response")
	}
	if len(data) > maxAvatarBytes {
		return nil, "", fmt.Errorf("larger than %d bytes", maxAvatarBytes)
	}
	return data, contentType, nil
}

// sizedAvatarURL asks GitHub for a 64px avatar (2x the displayed 32px)
// instead of the full-size image, which is often hundreds of KiB. Other
// forges' URLs are returned unchanged.
func sizedAvatarURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Host != "github.com" && parsed.Host != "avatars.githubusercontent.com") {
		return u
	}
	q := parsed.Query()
	q.Set("size", "64")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// imageFilename is the last path segment of u (e.g. "owner.png"), used as
// the upload's filename hint.
func imageFilename(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	if base := path.Base(parsed.Path); base != "." && base != "/" {
		return base
	}
	return ""
}
