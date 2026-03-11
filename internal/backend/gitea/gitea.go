package gitea

import (
	"fmt"
	"net/http"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
)

type Gitea struct {
	instanceURL string
	token       string
	client      *http.Client
}

func New(instanceURL, token string) *Gitea {
	return &Gitea{
		instanceURL: instanceURL,
		token:       token,
		client:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *Gitea) Name() string { return "gitea" }

func (g *Gitea) get(path string, out interface{}) error {
	req, err := http.NewRequest("GET", g.instanceURL+path, nil)
	if err != nil {
		return err
	}
	if g.token != "" {
		req.Header.Set("Authorization", "token "+g.token)
	}
	return backend.DoJSON(g.client, req, out)
}

type giteaRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Prerelease  bool      `json:"prerelease"`
}

type giteaTag struct {
	Name string `json:"name"`
}

type giteaRepo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

type giteaRepoInfo struct {
	Owner struct {
		AvatarURL string `json:"avatar_url"`
	} `json:"owner"`
}

func (g *Gitea) getOwnerAvatarURL(slug string) string {
	var info giteaRepoInfo
	if err := g.get("/api/v1/repos/"+slug, &info); err != nil {
		return ""
	}
	return info.Owner.AvatarURL
}

func (g *Gitea) GetRepoReleases(slug string, limit int) ([]backend.Release, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/releases?limit=%d", slug, limit)
	var releases []giteaRelease
	if err := g.get(path, &releases); err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return g.getRepoTags(slug, limit)
	}

	avatarURL := g.getOwnerAvatarURL(slug)
	repoURL := g.instanceURL + "/" + slug
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
			AvatarURL:    avatarURL,
		})
	}
	return result, nil
}

func (g *Gitea) getRepoTags(slug string, limit int) ([]backend.Release, error) {
	path := fmt.Sprintf("/api/v1/repos/%s/tags?limit=%d", slug, limit)
	var tags []giteaTag
	if err := g.get(path, &tags); err != nil {
		return nil, err
	}

	repoURL := g.instanceURL + "/" + slug
	result := make([]backend.Release, 0, len(tags))
	for _, t := range tags {
		tagURL := fmt.Sprintf("%s/releases/tag/%s", repoURL, t.Name)
		result = append(result, backend.Release{
			RepoSlug:    slug,
			RepoURL:     repoURL,
			TagName:     t.Name,
			Name:        t.Name,
			PublishedAt: time.Time{},
			URL:         tagURL,
		})
	}
	return result, nil
}

func (g *Gitea) GetUserStarredRepos(username string) ([]string, error) {
	path := fmt.Sprintf("/api/v1/users/%s/starred", username)
	var repos []giteaRepo
	if err := g.get(path, &repos); err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(repos))
	for _, r := range repos {
		slugs = append(slugs, r.FullName)
	}
	return slugs, nil
}

func (g *Gitea) GetOrgRepos(org string) ([]string, error) {
	path := fmt.Sprintf("/api/v1/orgs/%s/repos", org)
	var repos []giteaRepo
	if err := g.get(path, &repos); err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(repos))
	for _, r := range repos {
		slugs = append(slugs, r.FullName)
	}
	return slugs, nil
}

func (g *Gitea) GetGroupRepos(group string) ([]string, error) {
	return g.GetOrgRepos(group)
}
