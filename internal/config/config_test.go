package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFromString writes yaml to a temp file and runs Load on it.
func loadFromString(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return Load(path)
}

const minimalConfig = `
xmpp:
  jid: "bot@example.org"
  password: "secret"
`

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := loadFromString(t, minimalConfig)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.XMPP.Server != "example.org:5222" {
		t.Errorf("server = %q, want example.org:5222 derived from JID", cfg.XMPP.Server)
	}
	if cfg.XMPP.MUCNick != "releasebot" {
		t.Errorf("muc_nick = %q, want default releasebot", cfg.XMPP.MUCNick)
	}
	if cfg.Database.Path != "./releasetracker.db" {
		t.Errorf("database.path = %q, want default", cfg.Database.Path)
	}
	if cfg.Interval != 3600 {
		t.Errorf("interval = %d, want default 3600", cfg.Interval)
	}
}

func TestLoadExplicitValuesNotOverridden(t *testing.T) {
	cfg, err := loadFromString(t, `
xmpp:
  jid: "bot@example.org"
  password: "secret"
  server: "xmpp.example.org:5223"
  muc_nick: "tracker"
database:
  path: "/data/db.sqlite"
interval: 60
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.XMPP.Server != "xmpp.example.org:5223" || cfg.XMPP.MUCNick != "tracker" ||
		cfg.Database.Path != "/data/db.sqlite" || cfg.Interval != 60 {
		t.Errorf("explicit values were overridden: %+v", cfg)
	}
}

func TestLoadValidTrackingEntries(t *testing.T) {
	cfg, err := loadFromString(t, minimalConfig+`
default_notify:
  - jid: "room@muc.example.org"
    type: "muc"
tracking:
  - type: repo
    backend: github
    slug: "owner/repo"
  - type: user_stars
    backend: github
    username: "owner"
  - type: org
    backend: gitea
    org: "myorg"
  - type: group
    backend: gitlab
    group: "mygroup"
    notify:
      - jid: "user@example.org"
        type: "direct"
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tracking) != 4 {
		t.Errorf("got %d tracking entries, want 4", len(cfg.Tracking))
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			"missing jid",
			`{xmpp: {password: "x"}}`,
			"xmpp.jid is required",
		},
		{
			"missing password",
			`{xmpp: {jid: "bot@example.org"}}`,
			"xmpp.password is required",
		},
		{
			"jid without domain and no server",
			`{xmpp: {jid: "bot", password: "x"}}`,
			"user@domain",
		},
		{
			"unknown backend",
			minimalConfig + `
tracking:
  - {type: repo, backend: codeberg, slug: "a/b"}
`,
			`unknown backend "codeberg"`,
		},
		{
			"unknown type",
			minimalConfig + `
tracking:
  - {type: repository, backend: github, slug: "a/b"}
`,
			`unknown type "repository"`,
		},
		{
			"repo without slug",
			minimalConfig + `
tracking:
  - {type: repo, backend: github}
`,
			"requires the slug field",
		},
		{
			"user_stars without username",
			minimalConfig + `
tracking:
  - {type: user_stars, backend: github}
`,
			"requires the username field",
		},
		{
			"notify target without jid",
			minimalConfig + `
tracking:
  - type: repo
    backend: github
    slug: "a/b"
    notify:
      - {type: muc}
`,
			"jid is required",
		},
		{
			"notify target with bad type",
			minimalConfig + `
default_notify:
  - {jid: "room@muc.example.org", type: groupchat}
`,
			`unknown type "groupchat"`,
		},
		{
			"unparseable yaml",
			"xmpp: [",
			"parsing config file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadFromString(t, tt.yaml)
			if err == nil {
				t.Fatalf("Load succeeded, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Error("Load on a missing file should fail")
	}
}
