package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"tags removed", "<p>hello <b>world</b></p>", "hello world"},
		{"entities decoded", "a &amp; b &lt;c&gt; &quot;d&quot; &#39;e&#39;", `a & b <c> "d" 'e'`},
		// A literal "&lt;" written by the author arrives double-encoded as
		// "&amp;lt;" and must decode to "&lt;", not "<".
		{"no double decoding", "use &amp;lt; for less-than", "use &lt; for less-than"},
		{"named entities beyond the basic five", "caf&eacute; &mdash; ok", "café — ok"},
		{"whitespace trimmed", "  <p>x</p>  ", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHTML(tt.in); got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

const testAtomFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1/v2.0.0-rc1</id>
    <updated>2026-06-01T00:00:00Z</updated>
    <title>v2.0.0-rc1</title>
    <content type="html">&lt;p&gt;atom notes&lt;/p&gt;</content>
    <link rel="alternate" href="https://github.com/owner/repo/releases/tag/v2.0.0-rc1"/>
  </entry>
</feed>`

// testAPIReleases contains a stable-looking tag marked prerelease (the case
// the Atom heuristic gets wrong) and a draft that must be filtered out.
const testAPIReleases = `[
  {"tag_name":"v2.0.0","name":"Clean tag","prerelease":true,"draft":false,
   "html_url":"https://github.com/owner/repo/releases/tag/v2.0.0",
   "published_at":"2026-06-01T00:00:00Z","body":"api notes"},
  {"tag_name":"v2.0.1","name":"Unpublished","prerelease":false,"draft":true,
   "html_url":"https://github.com/owner/repo/releases/tag/v2.0.1",
   "published_at":null,"body":""}
]`

// newTestGitHub returns a backend pointed at two httptest servers: one playing
// api.github.com (using apiHandler) and one playing github.com (serving the
// Atom feed). It also returns counters for how often each was hit.
func newTestGitHub(t *testing.T, token string, apiHandler http.HandlerFunc) (g *GitHub, apiHits, atomHits *atomic.Int32) {
	t.Helper()
	apiHits, atomHits = &atomic.Int32{}, &atomic.Int32{}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		apiHandler(w, r)
	}))
	t.Cleanup(api.Close)

	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomHits.Add(1)
		if r.URL.Path != "/owner/repo/releases.atom" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, testAtomFeed)
	}))
	t.Cleanup(web.Close)

	g = New(token)
	g.apiBase = api.URL
	g.webBase = web.URL
	return g, apiHits, atomHits
}

func TestGetRepoReleasesUsesAPIWithToken(t *testing.T) {
	g, _, atomHits := newTestGitHub(t, "tok", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", got)
		}
		fmt.Fprint(w, testAPIReleases)
	})

	releases, err := g.GetRepoReleases("owner/repo", 5)
	if err != nil {
		t.Fatalf("GetRepoReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1 (draft must be filtered): %+v", len(releases), releases)
	}
	r := releases[0]
	if r.TagName != "v2.0.0" || !r.IsPrerelease {
		t.Errorf("got tag %q prerelease=%v, want v2.0.0 with authoritative prerelease=true", r.TagName, r.IsPrerelease)
	}
	if r.Body != "api notes" {
		t.Errorf("body = %q, want API markdown body", r.Body)
	}
	if atomHits.Load() != 0 {
		t.Errorf("Atom feed was hit %d times, want 0", atomHits.Load())
	}
}

func TestGetRepoReleasesFallsBackToAtomWhenRateLimited(t *testing.T) {
	g, apiHits, atomHits := newTestGitHub(t, "tok", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	})

	releases, err := g.GetRepoReleases("owner/repo", 5)
	if err != nil {
		t.Fatalf("GetRepoReleases: %v", err)
	}
	if apiHits.Load() != 1 || atomHits.Load() != 1 {
		t.Errorf("hits api=%d atom=%d, want 1 and 1", apiHits.Load(), atomHits.Load())
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1: %+v", len(releases), releases)
	}
	r := releases[0]
	// Atom path: tag from the link URL, heuristic prerelease detection.
	if r.TagName != "v2.0.0-rc1" || !r.IsPrerelease {
		t.Errorf("got tag %q prerelease=%v, want v2.0.0-rc1 with heuristic prerelease=true", r.TagName, r.IsPrerelease)
	}
	if r.Body != "atom notes" {
		t.Errorf("body = %q, want stripped Atom content", r.Body)
	}
}

func TestGetRepoReleasesAtomOnlyWithoutToken(t *testing.T) {
	g, apiHits, _ := newTestGitHub(t, "", func(w http.ResponseWriter, r *http.Request) {
		t.Error("API must not be called without a token")
	})

	releases, err := g.GetRepoReleases("owner/repo", 5)
	if err != nil {
		t.Fatalf("GetRepoReleases: %v", err)
	}
	if apiHits.Load() != 0 {
		t.Errorf("API was hit %d times, want 0", apiHits.Load())
	}
	if len(releases) != 1 || releases[0].TagName != "v2.0.0-rc1" {
		t.Errorf("unexpected releases: %+v", releases)
	}
}

func TestGetUserStarredReposPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/someone/starred" {
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			fmt.Fprint(w, `[`+repoListJSON(100)+`]`)
		case "2":
			fmt.Fprint(w, `[`+repoListJSON(30)+`]`)
		default:
			t.Errorf("unexpected page %q requested", page)
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	g := New("")
	g.apiBase = srv.URL
	slugs, err := g.GetUserStarredRepos("someone")
	if err != nil {
		t.Fatalf("GetUserStarredRepos: %v", err)
	}
	if len(slugs) != 130 {
		t.Errorf("got %d slugs, want 130 across two pages", len(slugs))
	}
}

// repoListJSON returns n comma-separated repo objects.
func repoListJSON(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"full_name":"owner/repo%d"}`, i)
	}
	return strings.Join(items, ",")
}

func TestGetRepoReleasesAPIErrorIsNotSwallowed(t *testing.T) {
	g, _, atomHits := newTestGitHub(t, "tok", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if _, err := g.GetRepoReleases("owner/repo", 5); err == nil {
		t.Fatal("want error on non-rate-limit API failure, got nil")
	}
	if atomHits.Load() != 0 {
		t.Errorf("Atom feed was hit %d times on a non-rate-limit error, want 0", atomHits.Load())
	}
}
