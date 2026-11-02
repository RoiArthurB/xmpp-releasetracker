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

// MatrixConfig holds the bot's Matrix account. Authenticate with either an
// access token (recommended) or a password; with a password the bot logs in at
// startup and logs out on shutdown so it does not accumulate stale devices.
type MatrixConfig struct {
	Homeserver  string `yaml:"homeserver"`   // base URL; discovered from user_id via .well-known when empty
	UserID      string `yaml:"user_id"`      // @bot:example.org
	AccessToken string `yaml:"access_token"` // takes precedence over password
	Password    string `yaml:"password"`
	DeviceID    string `yaml:"device_id"` // optional; reused on password login
}

// Configured reports whether a Matrix section was provided at all.
func (m MatrixConfig) Configured() bool {
	return m.Homeserver != "" || m.UserID != "" || m.AccessToken != "" || m.Password != ""
}

// Configured reports whether an XMPP section was provided at all.
func (x XMPPConfig) Configured() bool {
	return x.JID != "" || x.Password != "" || x.Server != ""
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

// Notify target types.
const (
	NotifyMUC          = "muc"           // XMPP multi-user chat room
	NotifyDirect       = "direct"        // XMPP 1:1 message
	NotifyMatrixRoom   = "matrix_room"   // Matrix room, by !room_id or #alias:server
	NotifyMatrixDirect = "matrix_direct" // Matrix DM, by @user:server
)

type NotifyTarget struct {
	JID  string `yaml:"jid"`  // XMPP address, for muc and direct
	ID   string `yaml:"id"`   // Matrix room ID/alias or user ID, for matrix_room and matrix_direct
	Type string `yaml:"type"` // see the Notify* constants
}

// IsMatrix reports whether the target is delivered over Matrix.
func (t NotifyTarget) IsMatrix() bool {
	return t.Type == NotifyMatrixRoom || t.Type == NotifyMatrixDirect
}

// Address returns the destination: the Matrix ID for Matrix targets, the JID
// otherwise.
func (t NotifyTarget) Address() string {
	if t.IsMatrix() {
		return t.ID
	}
	return t.JID
}

type TrackingEntry struct {
	Type            string         `yaml:"type"`     // "repo", "user_stars", "org", "group", "user"
	Backend         string         `yaml:"backend"`  // "github", "gitlab", "gitea"
	Slug            string         `yaml:"slug"`     // for type=repo
	Username        string         `yaml:"username"` // for type=user_stars
	User            string         `yaml:"user"`     // for type=user
	Org             string         `yaml:"org"`      // for type=org
	Group           string         `yaml:"group"`    // for type=group
	Instance        string         `yaml:"instance"` // GitLab/Gitea instance URL
	Notify          []NotifyTarget `yaml:"notify"`
	SkipPrereleases bool           `yaml:"skip_prereleases"` // if true, pre-releases are not announced
}

type Config struct {
	XMPP            XMPPConfig      `yaml:"xmpp"`
	Matrix          MatrixConfig    `yaml:"matrix"`
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
	if !cfg.XMPP.Configured() && !cfg.Matrix.Configured() {
		return fmt.Errorf("no chat protocol configured: set up an xmpp and/or a matrix section")
	}
	if cfg.XMPP.Configured() {
		if err := validateXMPP(&cfg.XMPP); err != nil {
			return err
		}
	}
	if cfg.Matrix.Configured() {
		if err := validateMatrix(&cfg.Matrix); err != nil {
			return err
		}
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "./releasetracker.db"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 3600
	}
	if err := validateNotify(cfg, cfg.DefaultNotify, "default_notify"); err != nil {
		return err
	}
	for i := range cfg.Tracking {
		if err := validateTrackingEntry(cfg, &cfg.Tracking[i], i); err != nil {
			return err
		}
	}
	return nil
}

func validateXMPP(x *XMPPConfig) error {
	if x.JID == "" {
		return fmt.Errorf("xmpp.jid is required")
	}
	if x.Password == "" {
		return fmt.Errorf("xmpp.password is required")
	}
	if x.MUCNick == "" {
		x.MUCNick = "releasebot"
	}
	if x.Server == "" {
		// derive server from JID domain
		parts := strings.SplitN(x.JID, "@", 2)
		if len(parts) != 2 {
			return fmt.Errorf("xmpp.jid must be in user@domain format")
		}
		x.Server = parts[1] + ":5222"
	}
	return nil
}

func validateMatrix(m *MatrixConfig) error {
	if m.UserID == "" {
		return fmt.Errorf("matrix.user_id is required")
	}
	if !isMatrixUserID(m.UserID) {
		return fmt.Errorf("matrix.user_id %q must be in @user:server format", m.UserID)
	}
	if m.AccessToken == "" && m.Password == "" {
		return fmt.Errorf("matrix.access_token or matrix.password is required")
	}
	m.Homeserver = strings.TrimRight(m.Homeserver, "/")
	if m.Homeserver != "" && !strings.HasPrefix(m.Homeserver, "https://") && !strings.HasPrefix(m.Homeserver, "http://") {
		return fmt.Errorf("matrix.homeserver %q must start with https:// or http://", m.Homeserver)
	}
	return nil
}

// isMatrixUserID checks the @localpart:server shape of a Matrix user ID.
func isMatrixUserID(s string) bool {
	local, server, ok := strings.Cut(strings.TrimPrefix(s, "@"), ":")
	return strings.HasPrefix(s, "@") && ok && local != "" && server != ""
}

// isMatrixRoom accepts a room ID (!opaque, optionally :server; room version 12
// IDs have no server part) or a room alias (#name:server).
func isMatrixRoom(s string) bool {
	switch {
	case strings.HasPrefix(s, "!"):
		return len(s) > 1
	case strings.HasPrefix(s, "#"):
		name, server, ok := strings.Cut(s[1:], ":")
		return ok && name != "" && server != ""
	}
	return false
}

// validateTrackingEntry checks the entry's backend, type, required field for
// that type, and notify targets, so typos fail at startup instead of being
// logged once per poll cycle.
func validateTrackingEntry(cfg *Config, e *TrackingEntry, i int) error {
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
	case "user":
		required, value = "user", e.User
	default:
		return fmt.Errorf("tracking[%d]: unknown type %q (expected repo, user_stars, org, group or user)", i, e.Type)
	}
	if value == "" {
		return fmt.Errorf("tracking[%d]: type %q requires the %s field", i, e.Type, required)
	}
	return validateNotify(cfg, e.Notify, fmt.Sprintf("tracking[%d].notify", i))
}

// validateNotify checks that every notify target has a known type, the
// address field that type uses, and that its protocol is configured.
func validateNotify(cfg *Config, targets []NotifyTarget, where string) error {
	for i, t := range targets {
		switch t.Type {
		case NotifyMUC, NotifyDirect:
			if t.JID == "" {
				return fmt.Errorf("%s[%d]: jid is required", where, i)
			}
			if !cfg.XMPP.Configured() {
				return fmt.Errorf("%s[%d]: %s target %q needs an xmpp section", where, i, t.Type, t.JID)
			}
		case NotifyMatrixRoom, NotifyMatrixDirect:
			if t.ID == "" {
				return fmt.Errorf("%s[%d]: id is required for %s targets", where, i, t.Type)
			}
			if t.Type == NotifyMatrixRoom && !isMatrixRoom(t.ID) {
				return fmt.Errorf("%s[%d]: %q is not a Matrix room ID (!id) or alias (#name:server)", where, i, t.ID)
			}
			if t.Type == NotifyMatrixDirect && !isMatrixUserID(t.ID) {
				return fmt.Errorf("%s[%d]: %q is not a Matrix user ID (@user:server)", where, i, t.ID)
			}
			if !cfg.Matrix.Configured() {
				return fmt.Errorf("%s[%d]: %s target %q needs a matrix section", where, i, t.Type, t.ID)
			}
		default:
			return fmt.Errorf("%s[%d]: unknown type %q (expected muc, direct, matrix_room or matrix_direct)", where, i, t.Type)
		}
	}
	return nil
}
