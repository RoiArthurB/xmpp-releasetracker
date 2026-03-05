# xmpp-releasetracker

A Go bot that watches repositories on GitHub, GitLab, and Gitea for new releases or tags, and announces them via XMPP — both to MUC rooms and to individual users.

## Features

- Monitors **GitHub**, **GitLab** (self-hosted or gitlab.com), and **Gitea** instances
- Tracks individual repos, user starred repos, organizations, and GitLab groups
- Sends notifications to **XMPP MUC rooms** and **direct messages**
- Persists the last-seen release in SQLite — no duplicate announcements after a restart
- Silent on first run: records the current latest release without announcing it
- Pure Go, no CGO required

## Requirements

- Go 1.21+
- An XMPP account for the bot

## Installation

```bash
git clone https://your-forge.example.com/you/xmpp-releasetracker
cd xmpp-releasetracker
go build -o xmpp-releasetracker .
```

## Configuration

Copy the example config and edit it:

```bash
cp config.yml.example config.yml
$EDITOR config.yml
```

### Full reference

```yaml
xmpp:
  jid: "bot@example.com"        # Bot's JID
  password: "secret"
  server: "example.com:5222"    # Optional. Defaults to the domain part of the JID on port 5222
  muc_nick: "releasebot"        # Nickname used when joining MUC rooms

backends:
  github:
    token: "ghp_xxx"            # Optional, but strongly recommended to avoid rate limiting
  gitlab:
    - url: "https://gitlab.com"
      token: "glpat-xxx"
    - url: "https://gitlab.example.com"   # Self-hosted instance
      token: "glpat-yyy"
  gitea:
    - url: "https://gitea.example.com"
      token: "xxx"

database:
  path: "./releasetracker.db"   # Default: ./releasetracker.db

interval: 3600                  # Seconds between poll cycles. Default: 3600

tracking:
  - ...                         # See "Tracking entries" below
```

### Tracking entries

Each entry in `tracking` describes what to watch and where to send notifications.

#### Single repository

```yaml
- type: repo
  backend: github               # github, gitlab, or gitea
  slug: "owner/repo"
  notify:
    - jid: "room@conference.example.com"
      type: muc
    - jid: "user@example.com"
      type: direct
```

For GitLab and Gitea, add an `instance` field to select which configured instance to use:

```yaml
- type: repo
  backend: gitlab
  slug: "group/project"
  instance: "https://gitlab.example.com"
  notify:
    - jid: "room@conference.example.com"
      type: muc
```

#### User starred repositories

Watches all repositories starred by a given user:

```yaml
- type: user_stars
  backend: github
  username: "someuser"
  notify:
    - jid: "room@conference.example.com"
      type: muc
```

#### Organization repositories (GitHub / Gitea)

Watches all repositories belonging to an organization:

```yaml
- type: org
  backend: github
  org: "golang"
  notify:
    - jid: "room@conference.example.com"
      type: muc
```

#### Group repositories (GitLab)

Watches all projects inside a GitLab group:

```yaml
- type: group
  backend: gitlab
  group: "gitlab-org"
  instance: "https://gitlab.com"
  notify:
    - jid: "room@conference.example.com"
      type: muc
```

### Notification targets

| Field  | Values              | Description                        |
|--------|---------------------|------------------------------------|
| `jid`  | any JID             | Destination address                |
| `type` | `muc` or `direct`   | MUC room or direct (1:1) message   |

## Usage

```bash
./xmpp-releasetracker -config config.yml
```

The `-config` flag defaults to `config.yml` in the current directory.

## Notification format

```
[Github] owner/repo — v1.2.3 "Release name"
https://github.com/owner/repo/releases/tag/v1.2.3

Release notes here, capped at 10 lines / 2000 characters...
```

## How it works

On each poll cycle the tracker:

1. Resolves the list of repos for each tracking entry (direct slug, or expanded from stars/org/group)
2. Fetches the latest releases from the backend API (up to 5 per repo)
3. Compares against the last-seen release stored in SQLite
4. Announces any releases newer than the last-seen entry, in chronological order
5. Updates the last-seen record

**First run:** the latest release is recorded silently, with no announcement. This prevents flooding notifications for repos that already have many releases.

## Project structure

```
main.go
config.yml.example
internal/
  config/       # YAML loading and validation
  store/        # SQLite persistence (last-seen releases)
  backend/
    backend.go  # Backend interface and Release type
    github/     # GitHub REST API
    gitlab/     # GitLab REST API
    gitea/      # Gitea REST API
  tracker/      # Polling loop and notification logic
  xmpp/         # XMPP connection, MUC join, message sending
```

## License

MIT
