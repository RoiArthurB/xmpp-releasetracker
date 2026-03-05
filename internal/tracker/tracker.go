package tracker

import (
	"bytes"
	"encoding/xml"
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

	// Sort ascending by published_at so we announce in order.
	sort.Slice(releases, func(i, j int) bool {
		return releases[i].PublishedAt.Before(releases[j].PublishedAt)
	})

	// Filter: releases strictly newer than last_seen.
	// When lastSeen is nil (first run for this repo), all fetched releases are announced.
	var newReleases []backend.Release
	for _, r := range releases {
		if lastSeen != nil && r.TagName == lastSeen.TagName {
			continue
		}
		if lastSeen != nil && !r.PublishedAt.IsZero() && !r.PublishedAt.After(lastSeen.PublishedAt) {
			continue
		}
		newReleases = append(newReleases, r)
	}

	for _, r := range newReleases {
		plain, html := formatRelease(b.Name(), r)
		for _, target := range notify {
			if err := t.sendNotification(target, plain, html); err != nil {
				log.Printf("Sending notification to %s: %v", target.JID, err)
			}
		}
	}

	// Update last_seen to the most recent release we processed.
	if len(newReleases) > 0 {
		latest := newReleases[len(newReleases)-1]
		if err := t.store.SetLastSeen(b.Name(), slug, latest.TagName, latest.PublishedAt); err != nil {
			return fmt.Errorf("updating last_seen: %w", err)
		}
	}

	return nil
}

func (t *Tracker) sendNotification(target config.NotifyTarget, plain, html string) error {
	switch target.Type {
	case "muc":
		return t.xmpp.SendMUC(target.JID, plain, html)
	case "direct":
		return t.xmpp.SendDirect(target.JID, plain, html)
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

// formatRelease returns a plain-text message and an XHTML-IM body for the given release.
func formatRelease(backendName string, r backend.Release) (plain, html string) {
	label := strings.ToUpper(backendName[:1]) + backendName[1:]

	title := r.TagName
	if r.Name != "" && r.Name != r.TagName {
		title = fmt.Sprintf("%s %q", r.TagName, r.Name)
	}

	// Plain text (unchanged behaviour)
	plain = fmt.Sprintf("[%s] %s — %s\n%s", label, r.RepoSlug, title, r.URL)
	if r.Body != "" {
		plain += "\n\n" + truncateBody(r.Body)
	}

	// XHTML-IM body
	html = buildHTML(r, title)
	return
}

// buildHTML constructs the XHTML-IM body for a release notification.
// It includes an avatar image when AvatarURL is set.
func buildHTML(r backend.Release, title string) string {
	var b strings.Builder

	// Header line: [avatar] repo-link — tag "name"
	b.WriteString("<p>")
	if r.AvatarURL != "" {
		fmt.Fprintf(&b, `<img src="%s" alt="avatar" height="32" width="32"/> `,
			xmlEscape(r.AvatarURL))
	}
	fmt.Fprintf(&b, `<a href="%s">%s</a> &#x2014; %s`,
		xmlEscape(r.RepoURL),
		xmlEscape(r.RepoSlug),
		xmlEscape(title),
	)
	b.WriteString("</p>")

	// Release URL
	fmt.Fprintf(&b, `<p><a href="%s">%s</a></p>`,
		xmlEscape(r.URL),
		xmlEscape(r.URL),
	)

	// Release body: escape text content, convert newlines to <br/>
	if r.Body != "" {
		body := truncateBody(r.Body)
		b.WriteString("<p>")
		b.WriteString(strings.ReplaceAll(xmlEscape(body), "\n", "<br/>"))
		b.WriteString("</p>")
	}

	return b.String()
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

// xmlEscape escapes s for use in XML text content or attribute values.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
