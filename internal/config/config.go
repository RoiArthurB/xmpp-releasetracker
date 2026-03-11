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
	Type     string         `yaml:"type"`     // "repo", "user_stars", "org", "group"
	Backend  string         `yaml:"backend"`  // "github", "gitlab", "gitea"
	Slug     string         `yaml:"slug"`     // for type=repo
	Username string         `yaml:"username"` // for type=user_stars
	Org      string         `yaml:"org"`      // for type=org
	Group    string         `yaml:"group"`    // for type=group
	Instance string         `yaml:"instance"` // GitLab/Gitea instance URL
	Notify   []NotifyTarget `yaml:"notify"`
}

type Config struct {
	XMPP          XMPPConfig      `yaml:"xmpp"`
	Backends      BackendsConfig  `yaml:"backends"`
	Database      DatabaseConfig  `yaml:"database"`
	Interval      int             `yaml:"interval"`
	Verbose       bool            `yaml:"verbose"`        // log 404s for repos without releases
	DefaultNotify []NotifyTarget  `yaml:"default_notify"` // optional; applied to every tracked repo
	Tracking      []TrackingEntry `yaml:"tracking"`
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
	return nil
}
