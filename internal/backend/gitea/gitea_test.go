package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRepoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/releases":
			// A clean tag explicitly flagged prerelease: Gitea's flag is
			// authoritative, no name heuristic involved.
			fmt.Fprint(w, `[
				{"tag_name":"v2.0.0","name":"Two point oh","body":"notes",
				 "published_at":"2026-06-01T00:00:00Z",
				 "html_url":"https://gitea.example.org/owner/repo/releases/tag/v2.0.0",
				 "prerelease":true}
			]`)
		case "/api/v1/repos/owner/repo":
			fmt.Fprint(w, `{"owner":{"avatar_url":"https://gitea.example.org/avatar.png"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	releases, err := g.GetRepoReleases("owner/repo", 5)
	if err != nil {
		t.Fatalf("GetRepoReleases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("got %d releases, want 1: %+v", len(releases), releases)
	}
	r := releases[0]
	if r.TagName != "v2.0.0" || !r.IsPrerelease {
		t.Errorf("got tag %q prerelease=%v, want v2.0.0 with authoritative prerelease=true", r.TagName, r.IsPrerelease)
	}
	if r.AvatarURL != "https://gitea.example.org/avatar.png" {
		t.Errorf("AvatarURL = %q, want owner avatar", r.AvatarURL)
	}
}

func TestGetRepoReleasesFallsBackToTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/releases":
			fmt.Fprint(w, `[]`)
		case "/api/v1/repos/owner/repo/tags":
			fmt.Fprint(w, `[{"name":"v1.0.0"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	releases, err := g.GetRepoReleases("owner/repo", 5)
	if err != nil {
		t.Fatalf("GetRepoReleases: %v", err)
	}
	if len(releases) != 1 || releases[0].TagName != "v1.0.0" {
		t.Fatalf("unexpected releases from tag fallback: %+v", releases)
	}
	if !releases[0].PublishedAt.IsZero() {
		t.Error("tag-only releases must have a zero timestamp")
	}
}

// repoListJSON returns a JSON array of n repos named "{prefix}/repo{i}".
func repoListJSON(t *testing.T, prefix string, offset, n int) string {
	t.Helper()
	type repo struct {
		FullName string `json:"full_name"`
	}
	repos := make([]repo, n)
	for i := range repos {
		repos[i].FullName = fmt.Sprintf("%s/repo%d", prefix, offset+i)
	}
	data, err := json.Marshal(repos)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestGetOrgReposPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/orgs/myorg/repos" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("limit"); got != fmt.Sprint(repoListPageSize) {
			t.Errorf("limit = %q, want %d", got, repoListPageSize)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, repoListJSON(t, "o", 0, repoListPageSize))
		case "2":
			fmt.Fprint(w, repoListJSON(t, "o", repoListPageSize, 20))
		default:
			t.Errorf("unexpected page %q requested", r.URL.Query().Get("page"))
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	slugs, err := g.GetOrgRepos("myorg")
	if err != nil {
		t.Fatalf("GetOrgRepos: %v", err)
	}
	if want := repoListPageSize + 20; len(slugs) != want {
		t.Errorf("got %d slugs, want %d across two pages", len(slugs), want)
	}
}

func TestGetUserStarredReposPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/someone/starred" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			// Exactly a full page: the client must request page 2 and stop
			// on the empty result.
			fmt.Fprint(w, repoListJSON(t, "u", 0, repoListPageSize))
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	slugs, err := g.GetUserStarredRepos("someone")
	if err != nil {
		t.Fatalf("GetUserStarredRepos: %v", err)
	}
	if len(slugs) != repoListPageSize {
		t.Errorf("got %d slugs, want %d", len(slugs), repoListPageSize)
	}
}
