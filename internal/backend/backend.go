package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound is returned when a repository or resource does not exist on the forge.
var ErrNotFound = errors.New("not found")

// ErrRateLimited is returned when the forge API rejects a request because the
// client exceeded its rate limit. Callers can detect it with errors.Is and
// fall back to an unmetered source where one exists.
var ErrRateLimited = errors.New("rate limited")

// prereleaseTokens are common version-suffix markers indicating a pre-release.
var prereleaseTokens = []string{
	"alpha", "beta", "rc", "pre", "preview",
	"dev", "snapshot", "nightly", "canary", "eap", "milestone",
}

// LooksLikePrerelease heuristically reports whether a tag/name denotes a
// pre-release, based on common pre-release identifiers (e.g. "v1.2.0-rc1").
// It is used by backends (GitHub, GitLab) whose release APIs do not expose an
// explicit prerelease flag; backends that do (Gitea) should use that instead.
func LooksLikePrerelease(tagName, name string) bool {
	for _, s := range []string{tagName, name} {
		lower := strings.ToLower(s)
		for _, tok := range prereleaseTokens {
			if containsToken(lower, tok) {
				return true
			}
		}
	}
	return false
}

// containsToken reports whether tok appears in s bounded by non-letters on
// both sides, so "rc" matches "1.0-rc1" but not "search" or "betamax".
func containsToken(s, tok string) bool {
	for from := 0; from <= len(s)-len(tok); {
		i := strings.Index(s[from:], tok)
		if i < 0 {
			return false
		}
		i += from
		leftOK := i == 0 || !isLetter(s[i-1])
		right := i + len(tok)
		rightOK := right == len(s) || !isLetter(s[right])
		if leftOK && rightOK {
			return true
		}
		from = i + 1
	}
	return false
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// CheckResponse validates an HTTP response status code. It returns ErrNotFound
// (wrapped) on 404, ErrRateLimited (wrapped) on rate-limit rejections, a
// generic error on any other non-200, and nil on 200.
func CheckResponse(resp *http.Response, url string) error {
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("HTTP GET %s: %w", url, ErrNotFound)
	}
	if isRateLimited(resp) {
		return fmt.Errorf("HTTP GET %s: status %d: %w", url, resp.StatusCode, ErrRateLimited)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// isRateLimited reports whether resp is a rate-limit rejection: a plain 429,
// or GitHub's primary (403 with X-RateLimit-Remaining: 0) and secondary
// (403 with Retry-After) rate-limit responses.
func isRateLimited(resp *http.Response) bool {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if resp.StatusCode == http.StatusForbidden {
		return resp.Header.Get("X-Ratelimit-Remaining") == "0" ||
			resp.Header.Get("Retry-After") != ""
	}
	return false
}

// DoJSON executes req, checks the response status, and JSON-decodes the body into out.
func DoJSON(client *http.Client, req *http.Request, out interface{}) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	if err := CheckResponse(resp, req.URL.String()); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Release represents a software release or tag from a repository.
type Release struct {
	RepoSlug     string
	RepoURL      string
	TagName      string
	Name         string
	PublishedAt  time.Time
	Body         string
	URL          string
	IsPrerelease bool
	AvatarURL    string // project icon or author/owner avatar; empty when unavailable
}

// Backend is the interface all forge backends must implement.
type Backend interface {
	Name() string
	GetRepoReleases(slug string, limit int) ([]Release, error)
	GetUserStarredRepos(username string) ([]string, error)
	GetOrgRepos(org string) ([]string, error)
	GetGroupRepos(group string) ([]string, error)
}
