package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRepoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slugs are path-escaped in the API URL (owner%2Frepo); EscapedPath
		// preserves that, while r.URL.Path would have decoded it.
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/owner%2Frepo/releases":
			fmt.Fprint(w, `[
				{"tag_name":"v1.0.0-rc1","name":"RC 1","description":"notes",
				 "released_at":"2026-06-01T00:00:00Z",
				 "_links":{"self":"https://gitlab.example.org/owner/repo/-/releases/v1.0.0-rc1"}},
				{"tag_name":"v0.9.0","name":"v0.9.0","description":"",
				 "released_at":"2026-05-01T00:00:00Z","_links":{"self":""}}
			]`)
		case "/api/v4/projects/owner%2Frepo":
			fmt.Fprint(w, `{"namespace":{"avatar_url":"https://gitlab.example.org/avatar.png"}}`)
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
	if len(releases) != 2 {
		t.Fatalf("got %d releases, want 2: %+v", len(releases), releases)
	}

	rc := releases[0]
	if rc.TagName != "v1.0.0-rc1" || !rc.IsPrerelease {
		t.Errorf("got tag %q prerelease=%v, want v1.0.0-rc1 detected as prerelease", rc.TagName, rc.IsPrerelease)
	}
	if rc.URL != "https://gitlab.example.org/owner/repo/-/releases/v1.0.0-rc1" {
		t.Errorf("URL = %q, want the _links.self value", rc.URL)
	}
	if rc.AvatarURL != "https://gitlab.example.org/avatar.png" {
		t.Errorf("AvatarURL = %q, want namespace avatar", rc.AvatarURL)
	}

	stable := releases[1]
	if stable.IsPrerelease {
		t.Error("v0.9.0 wrongly detected as prerelease")
	}
	// Empty _links.self must fall back to a constructed URL.
	if want := srv.URL + "/owner/repo/-/releases/v0.9.0"; stable.URL != want {
		t.Errorf("URL = %q, want constructed fallback %q", stable.URL, want)
	}
}

func TestGetRepoReleasesFallsBackToTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/owner%2Frepo/releases":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/owner%2Frepo/repository/tags":
			fmt.Fprint(w, `[{"name":"v1.0.0","commit":{"created_at":"2026-06-01T00:00:00Z"}}]`)
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
	if want := srv.URL + "/owner/repo/-/tags/v1.0.0"; releases[0].URL != want {
		t.Errorf("URL = %q, want %q", releases[0].URL, want)
	}
}

// repoListJSON returns a JSON array of n projects named "{prefix}/repo{i}".
func repoListJSON(t *testing.T, prefix string, offset, n int) string {
	t.Helper()
	type proj struct {
		PathWithNamespace string `json:"path_with_namespace"`
	}
	projects := make([]proj, n)
	for i := range projects {
		projects[i].PathWithNamespace = fmt.Sprintf("%s/repo%d", prefix, offset+i)
	}
	data, err := json.Marshal(projects)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestGetUserStarredReposPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/someone/starred_projects" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, repoListJSON(t, "u", 0, 100))
		case "2":
			fmt.Fprint(w, repoListJSON(t, "u", 100, 30))
		default:
			t.Errorf("unexpected page %q requested", r.URL.Query().Get("page"))
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	slugs, err := g.GetUserStarredRepos("someone")
	if err != nil {
		t.Fatalf("GetUserStarredRepos: %v", err)
	}
	if len(slugs) != 130 {
		t.Errorf("got %d slugs, want 130 across two pages", len(slugs))
	}
	if slugs[0] != "u/repo0" || slugs[129] != "u/repo129" {
		t.Errorf("unexpected slug ordering: first=%q last=%q", slugs[0], slugs[129])
	}
}

func TestGetGroupReposPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/groups/mygroup/projects" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "1":
			// Exactly a full page: the client must request page 2 and stop
			// on the empty result.
			fmt.Fprint(w, repoListJSON(t, "g", 0, 100))
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer srv.Close()

	g := New(srv.URL, "")
	slugs, err := g.GetGroupRepos("mygroup")
	if err != nil {
		t.Fatalf("GetGroupRepos: %v", err)
	}
	if len(slugs) != 100 {
		t.Errorf("got %d slugs, want 100", len(slugs))
	}
}
