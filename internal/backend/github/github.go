package github

import (
	"encoding/json"
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Prerelease  bool      `json:"prerelease"`
	Author      struct {
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
}

type ghTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
		URL string `json:"url"`
	} `json:"commit"`
}

type ghRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

func (g *GitHub) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", apiBase, slug, limit)
	var releases []ghRelease
	if err := g.get(url, &releases); err != nil {
		return nil, err
	}

	// Fallback to tags if no releases
	if len(releases) == 0 {
		return g.getRepoTags(slug, limit)
	}

	repoURL := fmt.Sprintf("https://github.com/%s", slug)
	result := make([]backend.Release, 0, len(releases))
	for _, r := range releases {
		result = append(result, backend.Release{
			RepoSlug:     slug,
			RepoURL:      repoURL,
			TagName:      r.TagName,
			Name:         r.Name,
			PublishedAt:  r.PublishedAt,
			Body:         r.Body,
			URL:          r.HTMLURL,
			IsPrerelease: r.Prerelease,
			AvatarURL:    r.Author.AvatarURL,
		})
	}
	return result, nil
}

func (g *GitHub) getRepoTags(slug string, limit int) ([]backend.Release, error) {
	url := fmt.Sprintf("%s/repos/%s/tags?per_page=%d", apiBase, slug, limit)
	var tags []ghTag
	if err := g.get(url, &tags); err != nil {
		return nil, err
	}

	owner := strings.SplitN(slug, "/", 2)[0]
	avatarURL := fmt.Sprintf("https://github.com/%s.png", owner)
	repoURL := fmt.Sprintf("https://github.com/%s", slug)
	result := make([]backend.Release, 0, len(tags))
	for _, t := range tags {
		tagURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", slug, t.Name)
		result = append(result, backend.Release{
			RepoSlug:    slug,
			RepoURL:     repoURL,
			TagName:     t.Name,
			Name:        t.Name,
			PublishedAt: time.Time{}, // tags don't have timestamps directly
			URL:         tagURL,
			AvatarURL:   avatarURL,
		})
	}
	return result, nil
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
