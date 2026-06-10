package backend

import (
	"errors"
	"net/http"
	"testing"
)

func TestLooksLikePrerelease(t *testing.T) {
	cases := []struct {
		tag, name string
		want      bool
	}{
		{"v1.0.0", "", false},
		{"1.2.3", "Release 1.2.3", false},
		{"v2.0.0-rc1", "", true},
		{"v2.0.0-rc.1", "", true},
		{"1.0.0-alpha", "", true},
		{"1.0.0-beta.2", "", true},
		{"v3.0.0-preview", "", true},
		{"v1.0.0", "1.0.0 Nightly", true},
		{"v1.0.0-SNAPSHOT", "", true},
		{"betamax-1.0", "", false}, // "beta" must be a bounded token
		{"v1.0.0-search", "", false},
		{"valpha", "", false}, // "alpha" followed by no boundary on left only
	}
	for _, c := range cases {
		if got := LooksLikePrerelease(c.tag, c.name); got != c.want {
			t.Errorf("LooksLikePrerelease(%q, %q) = %v, want %v", c.tag, c.name, got, c.want)
		}
	}
}

func TestCheckResponseRateLimited(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		headers map[string]string
		wantErr error
	}{
		{"ok", 200, nil, nil},
		{"not found", 404, nil, ErrNotFound},
		{"too many requests", 429, nil, ErrRateLimited},
		{"github primary limit", 403, map[string]string{"X-Ratelimit-Remaining": "0"}, ErrRateLimited},
		{"github secondary limit", 403, map[string]string{"Retry-After": "60"}, ErrRateLimited},
		{"plain forbidden is not rate limiting", 403, map[string]string{"X-Ratelimit-Remaining": "42"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: c.status, Header: http.Header{}}
			for k, v := range c.headers {
				resp.Header.Set(k, v)
			}
			err := CheckResponse(resp, "https://example.org")
			switch {
			case c.wantErr != nil:
				if !errors.Is(err, c.wantErr) {
					t.Errorf("status %d: err = %v, want %v", c.status, err, c.wantErr)
				}
			case c.status == 200:
				if err != nil {
					t.Errorf("status 200: err = %v, want nil", err)
				}
			default:
				// Non-200 without a sentinel must still be an error, just a generic one.
				if err == nil || errors.Is(err, ErrRateLimited) || errors.Is(err, ErrNotFound) {
					t.Errorf("status %d: err = %v, want generic error", c.status, err)
				}
			}
		})
	}
}
