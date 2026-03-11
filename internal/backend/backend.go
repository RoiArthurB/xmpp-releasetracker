package backend

import (
	"errors"
	"time"
)

// ErrNotFound is returned when a repository or resource does not exist on the forge.
var ErrNotFound = errors.New("not found")

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
