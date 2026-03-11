package github

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
)

const apiBase = "https://api.github.com"

type GitHub struct {
	token  string
	client *http.Client
}

func New(token string) *GitHub {
	return &GitHub{
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
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

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("HTTP GET %s: %w", url, backend.ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ghRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
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

func (g *GitHub) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	url := fmt.Sprintf("https://github.com/%s/releases.atom", slug)
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
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, backend.ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}

	var feed atomFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("parsing Atom feed: %w", err)
	}

	owner := strings.SplitN(slug, "/", 2)[0]
	avatarURL := fmt.Sprintf("https://github.com/%s.png", owner)
	repoURL := fmt.Sprintf("https://github.com/%s", slug)

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
			RepoSlug:    slug,
			RepoURL:     repoURL,
			TagName:     tagName,
			Name:        e.Title,
			PublishedAt: e.Updated.Time,
			Body:        stripHTML(e.Content),
			URL:         e.Link.Href,
			AvatarURL:   avatarURL,
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
	text := b.String()
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	return strings.TrimSpace(text)
}

func (g *GitHub) GetUserStarredRepos(username string) ([]string, error) {
	var slugs []string
	page := 1
	for {
		url := fmt.Sprintf("%s/users/%s/starred?per_page=100&page=%d", apiBase, username, page)
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
		url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d", apiBase, org, page)
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
