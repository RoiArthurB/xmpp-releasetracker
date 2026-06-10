package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type XMPPConfig struct {
	JID      string `yaml:"jid"`
	Password string `yaml:"password"`
	Server   string `yaml:"server"`
	MUCNick  string `yaml:"muc_nick"`
}

type GitHubConfig struct {
	Token string `yaml:"token"`
}

type GitLabInstanceConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

type GiteaInstanceConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
}

type BackendsConfig struct {
	GitHub GitHubConfig           `yaml:"github"`
	GitLab []GitLabInstanceConfig `yaml:"gitlab"`
	Gitea  []GiteaInstanceConfig  `yaml:"gitea"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type NotifyTarget struct {
	JID  string `yaml:"jid"`
	Type string `yaml:"type"` // "muc" or "direct"
}

type TrackingEntry struct {
	Type            string         `yaml:"type"`             // "repo", "user_stars", "org", "group"
	Backend         string         `yaml:"backend"`          // "github", "gitlab", "gitea"
	Slug            string         `yaml:"slug"`             // for type=repo
	Username        string         `yaml:"username"`         // for type=user_stars
	Org             string         `yaml:"org"`              // for type=org
	Group           string         `yaml:"group"`            // for type=group
	Instance        string         `yaml:"instance"`         // GitLab/Gitea instance URL
	Notify          []NotifyTarget `yaml:"notify"`
	SkipPrereleases bool           `yaml:"skip_prereleases"` // if true, pre-releases are not announced
}

type Config struct {
	XMPP            XMPPConfig      `yaml:"xmpp"`
	Backends        BackendsConfig  `yaml:"backends"`
	Database        DatabaseConfig  `yaml:"database"`
	Interval        int             `yaml:"interval"`
	Verbose         bool            `yaml:"verbose"`          // log 404s for repos without releases
	SkipPrereleases bool            `yaml:"skip_prereleases"` // global default; can be overridden per entry
	DefaultNotify   []NotifyTarget  `yaml:"default_notify"`   // optional; applied to every tracked repo
	Tracking        []TrackingEntry `yaml:"tracking"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.XMPP.JID == "" {
		return fmt.Errorf("xmpp.jid is required")
	}
	if cfg.XMPP.Password == "" {
		return fmt.Errorf("xmpp.password is required")
	}
	if cfg.XMPP.MUCNick == "" {
		cfg.XMPP.MUCNick = "releasebot"
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "./releasetracker.db"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 3600
	}
	if cfg.XMPP.Server == "" {
		// derive server from JID domain
		parts := strings.SplitN(cfg.XMPP.JID, "@", 2)
		if len(parts) != 2 {
			return fmt.Errorf("xmpp.jid must be in user@domain format")
		}
		cfg.XMPP.Server = parts[1] + ":5222"
	}
	if err := validateNotify(cfg.DefaultNotify, "default_notify"); err != nil {
		return err
	}
	for i := range cfg.Tracking {
		if err := validateTrackingEntry(&cfg.Tracking[i], i); err != nil {
			return err
		}
	}
	return nil
}

// validateTrackingEntry checks the entry's backend, type, required field for
// that type, and notify targets, so typos fail at startup instead of being
// logged once per poll cycle.
func validateTrackingEntry(e *TrackingEntry, i int) error {
	switch e.Backend {
	case "github", "gitlab", "gitea":
	default:
		return fmt.Errorf("tracking[%d]: unknown backend %q (expected github, gitlab or gitea)", i, e.Backend)
	}
	var required, value string
	switch e.Type {
	case "repo":
		required, value = "slug", e.Slug
	case "user_stars":
		required, value = "username", e.Username
	case "org":
		required, value = "org", e.Org
	case "group":
		required, value = "group", e.Group
	default:
		return fmt.Errorf("tracking[%d]: unknown type %q (expected repo, user_stars, org or group)", i, e.Type)
	}
	if value == "" {
		return fmt.Errorf("tracking[%d]: type %q requires the %s field", i, e.Type, required)
	}
	return validateNotify(e.Notify, fmt.Sprintf("tracking[%d].notify", i))
}

// validateNotify checks that every notify target has a JID and a known type.
func validateNotify(targets []NotifyTarget, where string) error {
	for i, t := range targets {
		if t.JID == "" {
			return fmt.Errorf("%s[%d]: jid is required", where, i)
		}
		if t.Type != "muc" && t.Type != "direct" {
			return fmt.Errorf("%s[%d]: unknown type %q (expected muc or direct)", where, i, t.Type)
		}
	}
	return nil
}
