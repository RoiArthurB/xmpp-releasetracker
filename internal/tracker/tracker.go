package tracker

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/roiarthurb/xmpp-releasetracker/internal/backend"
	"github.com/roiarthurb/xmpp-releasetracker/internal/config"
	"github.com/roiarthurb/xmpp-releasetracker/internal/store"
	"github.com/roiarthurb/xmpp-releasetracker/internal/xmpp"
)

const (
	maxBodyLines  = 10
	maxBodyChars  = 2000
	releasesLimit = 5
)

// BackendRegistry maps backend name → Backend instance.
type BackendRegistry map[string]backend.Backend

// Tracker orchestrates polling all tracking entries.
type Tracker struct {
	cfg      *config.Config
	backends BackendRegistry
	store    *store.Store
	xmpp     *xmpp.Client
}

func New(cfg *config.Config, backends BackendRegistry, st *store.Store, xc *xmpp.Client) *Tracker {
	return &Tracker{
		cfg:      cfg,
		backends: backends,
		store:    st,
		xmpp:     xc,
	}
}

// Run starts the polling loop; it blocks until the process exits.
func (t *Tracker) Run() {
	for {
		log.Println("Starting poll cycle...")
		t.poll()
		log.Printf("Poll cycle done. Sleeping %d seconds.", t.cfg.Interval)
		time.Sleep(time.Duration(t.cfg.Interval) * time.Second)
	}
}

func (t *Tracker) poll() {
	for _, entry := range t.cfg.Tracking {
		b, ok := t.backends[entry.Backend]
		if !ok {
			log.Printf("Unknown backend %q in tracking entry", entry.Backend)
			continue
		}

		slugs, err := t.resolveRepos(b, &entry)
		if err != nil {
			log.Printf("Resolving repos for entry %+v: %v", entry, err)
			continue
		}

		for _, slug := range slugs {
			if err := t.processRepo(b, slug, mergeNotify(t.cfg.DefaultNotify, entry.Notify)); err != nil {
				log.Printf("[%s] %s: %v", b.Name(), slug, err)
			}
		}
	}
}

func (t *Tracker) resolveRepos(b backend.Backend, entry *config.TrackingEntry) ([]string, error) {
	switch entry.Type {
	case "repo":
		return []string{entry.Slug}, nil
	case "user_stars":
		return b.GetUserStarredRepos(entry.Username)
	case "org":
		return b.GetOrgRepos(entry.Org)
	case "group":
		return b.GetGroupRepos(entry.Group)
	default:
		return nil, fmt.Errorf("unknown tracking type: %s", entry.Type)
	}
}

func (t *Tracker) processRepo(b backend.Backend, slug string, notify []config.NotifyTarget) error {
	releases, err := b.GetRepoReleases(slug, releasesLimit)
	if err != nil {
		return fmt.Errorf("fetching releases: %w", err)
	}
	if len(releases) == 0 {
		return nil
	}

	lastSeen, err := t.store.GetLastSeen(b.Name(), slug)
	if err != nil {
		return fmt.Errorf("loading last_seen: %w", err)
	}

	// First run: silently record the latest release without announcing.
	if lastSeen == nil {
		// APIs return newest first; also check by timestamp as a tiebreaker.
		latest := releases[0]
		for _, r := range releases[1:] {
			if r.PublishedAt.After(latest.PublishedAt) {
				latest = r
			}
		}
		return t.store.SetLastSeen(b.Name(), slug, latest.TagName, latest.PublishedAt)
	}

	// Determine new releases to announce.
	var newReleases []backend.Release
	if releases[0].PublishedAt.IsZero() || lastSeen.PublishedAt.IsZero() {
		// No usable timestamps (tag fallback or stale DB entry): use API position
		// as a recency proxy — APIs return newest first.
		// Find lastSeen; everything before it in the list is newer.
		lastSeenIdx := len(releases) // if not found, treat all as new
		for i, r := range releases {
			if r.TagName == lastSeen.TagName {
				lastSeenIdx = i
				break
			}
		}
		// Reverse so we announce oldest-first.
		for i := lastSeenIdx - 1; i >= 0; i-- {
			newReleases = append(newReleases, releases[i])
		}
	} else {
		// Both sides have timestamps: sort ascending and filter by time.
		sort.Slice(releases, func(i, j int) bool {
			return releases[i].PublishedAt.Before(releases[j].PublishedAt)
		})
		for _, r := range releases {
			if r.TagName == lastSeen.TagName {
				continue
			}
			if !r.PublishedAt.After(lastSeen.PublishedAt) {
				continue
			}
			newReleases = append(newReleases, r)
		}
	}

	for _, r := range newReleases {
		body, avatarURL := formatRelease(b.Name(), r)
		for _, target := range notify {
			if err := t.sendNotification(target, body, avatarURL); err != nil {
				log.Printf("Sending notification to %s: %v", target.JID, err)
			}
		}
	}

	// Update last_seen to the most recent release processed.
	if len(newReleases) > 0 {
		latest := newReleases[len(newReleases)-1]
		if err := t.store.SetLastSeen(b.Name(), slug, latest.TagName, latest.PublishedAt); err != nil {
			return fmt.Errorf("updating last_seen: %w", err)
		}
	}

	return nil
}

func (t *Tracker) sendNotification(target config.NotifyTarget, body, avatarURL string) error {
	switch target.Type {
	case "muc":
		return t.xmpp.SendMUC(target.JID, body, avatarURL)
	case "direct":
		return t.xmpp.SendDirect(target.JID, body, avatarURL)
	default:
		return fmt.Errorf("unknown notify type: %s", target.Type)
	}
}

// mergeNotify returns defaults plus any extra targets not already in defaults.
func mergeNotify(defaults, extras []config.NotifyTarget) []config.NotifyTarget {
	result := make([]config.NotifyTarget, len(defaults))
	copy(result, defaults)
	seen := make(map[string]struct{}, len(defaults))
	for _, t := range defaults {
		seen[t.JID+"|"+t.Type] = struct{}{}
	}
	for _, t := range extras {
		if _, ok := seen[t.JID+"|"+t.Type]; !ok {
			result = append(result, t)
		}
	}
	return result
}

// formatRelease returns the message body and the avatar URL for a release.
//
// Body format (XEP-0393 Message Styling):
//
//	https://avatar.url/image.png      ← first line when avatar available (XEP-0385 SIMS)
//	*[Github] owner/repo — v1.0.0*    ← bold via Message Styling
//	https://github.com/.../tag/v1.0.0
//
//	Release notes (truncated)...
//
// avatarURL is returned separately so the caller can pass it to the XMPP
// client, which uses it to set the correct begin/end offsets in the SIMS
// reference element.
func formatRelease(backendName string, r backend.Release) (body, avatarURL string) {
	label := strings.ToUpper(backendName[:1]) + backendName[1:]

	title := r.TagName
	if r.Name != "" && r.Name != r.TagName {
		title = r.TagName + " \u201c" + r.Name + "\u201d"
	}

	var b strings.Builder

	// XEP-0385: avatar URL must be the very first line of the body so the
	// SIMS reference can point to it at offset begin=0.
	if r.AvatarURL != "" {
		avatarURL = r.AvatarURL
		b.WriteString(r.AvatarURL)
		b.WriteByte('\n')
	}

	// XEP-0393: wrap the notification headline in *...* for bold rendering.
	fmt.Fprintf(&b, "*[%s] %s \u2014 %s*\n", label, r.RepoSlug, title)
	b.WriteString(r.URL)

	if r.Body != "" {
		b.WriteString("\n\n")
		b.WriteString(truncateBody(r.Body))
	}

	body = b.String()
	return
}

// truncateBody limits release notes to maxBodyLines lines and maxBodyChars characters.
func truncateBody(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > maxBodyLines {
		lines = lines[:maxBodyLines]
		lines = append(lines, "…")
	}
	result := strings.Join(lines, "\n")
	if len(result) > maxBodyChars {
		result = result[:maxBodyChars] + "…"
	}
	return result
}
