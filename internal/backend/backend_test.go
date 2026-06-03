package backend

import "testing"

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
