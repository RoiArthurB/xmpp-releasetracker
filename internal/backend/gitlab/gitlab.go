package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
)

type GitLab struct {
	instanceURL string
	token       string
	client      *http.Client
}

func New(instanceURL, token string) *GitLab {
	if instanceURL == "" {
		instanceURL = "https://gitlab.com"
	}
	return &GitLab{
		instanceURL: instanceURL,
		token:       token,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GitLab) Name() string { return "gitlab" }

func (g *GitLab) get(path string, out interface{}) error {
	reqURL := g.instanceURL + path
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return err
	}
	if g.token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.token)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP GET %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s: status %d", reqURL, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type glRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ReleasedAt  time.Time `json:"released_at"`
	Links       struct {
		Self string `json:"self"`
	} `json:"_links"`
}

type glTag struct {
	Name   string `json:"name"`
	Commit struct {
		CreatedAt time.Time `json:"created_at"`
	} `json:"commit"`
}

type glProject struct {
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

func (g *GitLab) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	encoded := url.PathEscape(slug)
	path := fmt.Sprintf("/api/v4/projects/%s/releases?per_page=%d", encoded, limit)
	var releases []glRelease
	if err := g.get(path, &releases); err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return g.getRepoTags(slug, limit)
	}

	repoURL := g.instanceURL + "/" + slug
	result := make([]backend.Release, 0, len(releases))
	for _, r := range releases {
		releaseURL := r.Links.Self
		if releaseURL == "" {
			releaseURL = fmt.Sprintf("%s/-/releases/%s", repoURL, r.TagName)
		}
		result = append(result, backend.Release{
			RepoSlug:    slug,
			RepoURL:     repoURL,
			TagName:     r.TagName,
			Name:        r.Name,
			PublishedAt: r.ReleasedAt,
			Body:        r.Description,
			URL:         releaseURL,
		})
	}
	return result, nil
}

func (g *GitLab) getRepoTags(slug string, limit int) ([]backend.Release, error) {
	encoded := url.PathEscape(slug)
	path := fmt.Sprintf("/api/v4/projects/%s/repository/tags?per_page=%d", encoded, limit)
	var tags []glTag
	if err := g.get(path, &tags); err != nil {
		return nil, err
	}

	repoURL := g.instanceURL + "/" + slug
	result := make([]backend.Release, 0, len(tags))
	for _, t := range tags {
		tagURL := fmt.Sprintf("%s/-/tags/%s", repoURL, t.Name)
		result = append(result, backend.Release{
			RepoSlug:    slug,
			RepoURL:     repoURL,
			TagName:     t.Name,
			Name:        t.Name,
			PublishedAt: t.Commit.CreatedAt,
			URL:         tagURL,
		})
	}
	return result, nil
}

func (g *GitLab) GetUserStarredRepos(username string) ([]string, error) {
	path := fmt.Sprintf("/api/v4/users/%s/starred_projects?per_page=100", username)
	var projects []glProject
	if err := g.get(path, &projects); err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(projects))
	for _, p := range projects {
		slugs = append(slugs, p.PathWithNamespace)
	}
	return slugs, nil
}

func (g *GitLab) GetOrgRepos(org string) ([]string, error) {
	return g.GetGroupRepos(org)
}

func (g *GitLab) GetGroupRepos(group string) ([]string, error) {
	var slugs []string
	page := 1
	for {
		path := fmt.Sprintf("/api/v4/groups/%s/projects?per_page=100&page=%d", url.PathEscape(group), page)
		var projects []glProject
		if err := g.get(path, &projects); err != nil {
			return nil, err
		}
		if len(projects) == 0 {
			break
		}
		for _, p := range projects {
			slugs = append(slugs, p.PathWithNamespace)
		}
		if len(projects) < 100 {
			break
		}
		page++
	}
	return slugs, nil
}
