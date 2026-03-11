package main

import (
	"flag"
	"log"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
	ghbackend "github.com/roiarthurb/xmpp-releasetracker/internal/backend/github"
	glbackend "github.com/roiarthurb/xmpp-releasetracker/internal/backend/gitlab"
	giteabackend "github.com/roiarthurb/xmpp-releasetracker/internal/backend/gitea"
	"github.com/roiarthurb/xmpp-releasetracker/internal/config"
	"github.com/roiarthurb/xmpp-releasetracker/internal/store"
	"github.com/roiarthurb/xmpp-releasetracker/internal/tracker"
	"github.com/roiarthurb/xmpp-releasetracker/internal/xmpp"
)

func main() {
	configPath := flag.String("config", "config.yml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Loading config: %v", err)
	}

	// Open database.
	st, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Opening database: %v", err)
	}
	defer st.Close()

	// Build backend registry.
	backends := make(tracker.BackendRegistry)

	if cfg.Backends.GitHub.Token != "" {
		backends["github"] = ghbackend.New(cfg.Backends.GitHub.Token)
	} else {
		// Allow unauthenticated GitHub access (rate-limited).
		backends["github"] = ghbackend.New("")
	}

	// GitLab: one backend per instance; keyed by instance URL.
	// Also register a default "gitlab" key pointing to gitlab.com if configured.
	for _, gl := range cfg.Backends.GitLab {
		b := glbackend.New(gl.URL, gl.Token)
		backends["gitlab:"+gl.URL] = b
		// Register "gitlab" as alias for first/only gitlab.com entry
		if gl.URL == "https://gitlab.com" || gl.URL == "https://gitlab.com/" {
			backends["gitlab"] = b
		}
	}
	// If no explicit gitlab.com entry but instances defined, use first as default "gitlab"
	if _, ok := backends["gitlab"]; !ok && len(cfg.Backends.GitLab) > 0 {
		backends["gitlab"] = glbackend.New(cfg.Backends.GitLab[0].URL, cfg.Backends.GitLab[0].Token)
	}

	// Gitea: one backend per instance.
	for _, gt := range cfg.Backends.Gitea {
		b := giteabackend.New(gt.URL, gt.Token)
		backends["gitea:"+gt.URL] = b
		if _, ok := backends["gitea"]; !ok {
			backends["gitea"] = b
		}
	}

	// Resolve instance-specific backends for tracking entries.
	// Entries with an "instance" field map to "backend:instanceURL".
	backends = resolveInstanceBackends(cfg, backends)

	// Connect to XMPP.
	log.Printf("Connecting to XMPP server %s as %s...", cfg.XMPP.Server, cfg.XMPP.JID)
	xc, err := xmpp.Connect(cfg.XMPP.JID, cfg.XMPP.Password, cfg.XMPP.Server, cfg.XMPP.MUCNick)
	if err != nil {
		log.Fatalf("XMPP connect: %v", err)
	}
	defer xc.Close()

	// Join all MUC rooms.
	rooms := collectMUCRooms(cfg)
	for _, room := range rooms {
		log.Printf("Joining MUC room: %s", room)
		if err := xc.JoinMUC(room); err != nil {
			log.Printf("Warning: could not join %s: %v", room, err)
		}
	}

	// Start tracker loop.
	t := tracker.New(cfg, backends, st, xc)
	t.Run()
}

// resolveInstanceBackends creates a backend entry for each tracking entry that
// specifies an instance, keyed by "backend" (the entry's Backend field), so the
// tracker can look it up directly.
func resolveInstanceBackends(cfg *config.Config, backends tracker.BackendRegistry) tracker.BackendRegistry {
	for i := range cfg.Tracking {
		entry := &cfg.Tracking[i]
		if entry.Instance == "" {
			continue
		}
		instanceKey := entry.Backend + ":" + entry.Instance
		if b, ok := backends[instanceKey]; ok {
			// Register a temporary key so the tracker uses the right instance.
			// We rewrite the entry's Backend to the instance-specific key.
			backends[instanceKey] = b
			entry.Backend = instanceKey
		}
	}
	return backends
}

// collectMUCRooms returns the unique set of MUC room JIDs from all tracking entries.
func collectMUCRooms(cfg *config.Config) []string {
	seen := make(map[string]struct{})
	var rooms []string
	for _, entry := range cfg.Tracking {
		for _, target := range entry.Notify {
			if target.Type == "muc" {
				if _, ok := seen[target.JID]; !ok {
					seen[target.JID] = struct{}{}
					rooms = append(rooms, target.JID)
				}
			}
		}
	}
	return rooms
}

// ensure backend.Backend is used (avoid import cycle lint issues)
var _ backend.Backend = nil
