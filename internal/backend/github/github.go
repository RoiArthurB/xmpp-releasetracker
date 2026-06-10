package github

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
)

const (
	defaultAPIBase = "https://api.github.com"
	defaultWebBase = "https://github.com"
)

type GitHub struct {
	token string
	// apiBase and webBase are fields rather than constants so tests can
	// point the backend at httptest servers.
	apiBase string
	webBase string
	client  *http.Client
}

func New(token string) *GitHub {
	return &GitHub{
		token:   token,
		apiBase: defaultAPIBase,
		webBase: defaultWebBase,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GitHub) Name() string { return "github" }

func (g *GitHub) get(url string, out interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return backend.DoJSON(g.client, req, out)
}

type ghRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

// ghRelease is a release object from the REST API, which — unlike the Atom
// feed — carries the authoritative prerelease and draft flags.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// atomFeed represents a GitHub releases Atom feed.
type atomFeed struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID      string   `xml:"id"`
	Updated atomTime `xml:"updated"`
	Title   string   `xml:"title"`
	Content string   `xml:"content"`
	Link    struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// atomTime wraps time.Time for RFC3339 XML unmarshalling.
type atomTime struct{ time.Time }

func (t *atomTime) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

// GetRepoReleases fetches releases via the REST API when a token is
// configured: its prerelease and draft flags are authoritative, where the
// Atom feed forces heuristic guessing from tag names. Without a token, or
// when the API reports rate limiting, it falls back to the unmetered Atom
// feed so notifications keep flowing.
func (g *GitHub) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	if g.token != "" {
		releases, err := g.getReleasesAPI(slug, limit)
		if err == nil {
			return releases, nil
		}
		if !errors.Is(err, backend.ErrRateLimited) {
			return nil, err
		}
		log.Printf("[github] %s: API rate limited, falling back to Atom feed", slug)
	}
	return g.getReleasesAtom(slug, limit)
}

func (g *GitHub) getReleasesAPI(slug string, limit int) ([]backend.Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", g.apiBase, slug, limit)
	var releases []ghRelease
	if err := g.get(url, &releases); err != nil {
		return nil, err
	}

	owner := strings.SplitN(slug, "/", 2)[0]
	avatarURL := fmt.Sprintf("%s/%s.png", g.webBase, owner)
	repoURL := fmt.Sprintf("%s/%s", g.webBase, slug)

	result := make([]backend.Release, 0, len(releases))
	for _, r := range releases {
		if r.Draft {
			continue
		}
		result = append(result, backend.Release{
			RepoSlug:     slug,
			RepoURL:      repoURL,
			TagName:      r.TagName,
			Name:         r.Name,
			PublishedAt:  r.PublishedAt,
			Body:         r.Body,
			URL:          r.HTMLURL,
			AvatarURL:    avatarURL,
			IsPrerelease: r.Prerelease,
		})
	}
	return result, nil
}

func (g *GitHub) getReleasesAtom(slug string, limit int) ([]backend.Release, error) {
	url := fmt.Sprintf("%s/%s/releases.atom", g.webBase, slug)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if err := backend.CheckResponse(resp, url); err != nil {
		return nil, err
	}

	var feed atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("parsing Atom feed: %w", err)
	}

	owner := strings.SplitN(slug, "/", 2)[0]
	avatarURL := fmt.Sprintf("%s/%s.png", g.webBase, owner)
	repoURL := fmt.Sprintf("%s/%s", g.webBase, slug)

	result := make([]backend.Release, 0, len(feed.Entries))
	for i, e := range feed.Entries {
		if i >= limit {
			break
		}
		// Tag name is the last path segment after /tag/ in the link URL.
		tagName := e.Link.Href
		if idx := strings.LastIndex(e.Link.Href, "/tag/"); idx >= 0 {
			tagName = e.Link.Href[idx+5:]
		}
		result = append(result, backend.Release{
			RepoSlug:     slug,
			RepoURL:      repoURL,
			TagName:      tagName,
			Name:         e.Title,
			PublishedAt:  e.Updated.Time,
			Body:         stripHTML(e.Content),
			URL:          e.Link.Href,
			AvatarURL:    avatarURL,
			// The releases.atom feed carries no prerelease flag, so infer it.
			IsPrerelease: backend.LooksLikePrerelease(tagName, e.Title),
		})
	}
	return result, nil
}

// stripHTML removes HTML tags and decodes common entities for use in plain-text messages.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	// html.UnescapeString decodes all entities in a single pass; the previous
	// ReplaceAll chain decoded "&amp;" first, turning "&amp;lt;" into "<".
	return strings.TrimSpace(html.UnescapeString(b.String()))
}

func (g *GitHub) GetUserStarredRepos(username string) ([]string, error) {
	var slugs []string
	page := 1
	for {
		url := fmt.Sprintf("%s/users/%s/starred?per_page=100&page=%d", g.apiBase, username, page)
		var repos []ghRepo
		if err := g.get(url, &repos); err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		for _, r := range repos {
			slugs = append(slugs, r.FullName)
		}
		if len(repos) < 100 {
			break
		}
		page++
	}
	return slugs, nil
}

func (g *GitHub) GetOrgRepos(org string) ([]string, error) {
	var slugs []string
	page := 1
	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d", g.apiBase, org, page)
		var repos []ghRepo
		if err := g.get(url, &repos); err != nil {
			return nil, err
		}
		if len(repos) == 0 {
			break
		}
		for _, r := range repos {
			slugs = append(slugs, r.FullName)
		}
		if len(repos) < 100 {
			break
		}
		page++
	}
	return slugs, nil
}

func (g *GitHub) GetGroupRepos(_ string) ([]string, error) {
	return nil, fmt.Errorf("GetGroupRepos not supported for GitHub backend; use GetOrgRepos")
}
