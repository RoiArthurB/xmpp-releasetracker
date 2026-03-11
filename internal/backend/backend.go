package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrNotFound is returned when a repository or resource does not exist on the forge.
var ErrNotFound = errors.New("not found")

// CheckResponse validates an HTTP response status code.
// It returns ErrNotFound (wrapped) on 404, a generic error on any other non-200, and nil on 200.
func CheckResponse(resp *http.Response, url string) error {
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("HTTP GET %s: %w", url, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}
	return nil
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
