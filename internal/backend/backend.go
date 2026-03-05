package backend

import "time"

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
