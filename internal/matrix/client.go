// Package matrix is a minimal Matrix Client-Server API client: just enough to
// join rooms, open direct chats and post notices. It talks plain HTTP and
// never syncs, so it cannot read encrypted rooms; messages it sends are
// unencrypted (clients show them, flagged as such, even in encrypted rooms).
package matrix

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxAttempts   = 3
	maxRetryAfter = 30 * time.Second
	directEvent   = "m.direct"
)

// Options configures Connect.
type Options struct {
	Homeserver  string // base URL; discovered from UserID via .well-known when empty
	UserID      string
	AccessToken string // preferred; Password is used only when this is empty
	Password    string
	DeviceID    string // optional; reused on password login
	HTTPClient  *http.Client
}

// Client posts messages to Matrix rooms. It is safe for concurrent use.
type Client struct {
	hs       string
	userID   string
	token    string
	loggedIn bool // true when we created the session and must log out on Close
	http     *http.Client
	txnBase  string
	txnSeq   atomic.Uint64

	mu      sync.Mutex
	rooms   map[string]string // room ID or alias → room ID
	directs map[string]string // user ID → DM room ID
}

// Error is a Matrix error response.
type Error struct {
	Status       int
	Code         string `json:"errcode"`
	Message      string `json:"error"`
	RetryAfterMS int64  `json:"retry_after_ms"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("matrix: HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

// Connect resolves the homeserver, authenticates and checks that the
// credentials belong to opts.UserID.
func Connect(opts Options) (*Client, error) {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	c := &Client{
		hs:      strings.TrimRight(opts.Homeserver, "/"),
		userID:  opts.UserID,
		token:   opts.AccessToken,
		http:    hc,
		txnBase: randomHex(8),
		rooms:   make(map[string]string),
		directs: make(map[string]string),
	}
	if c.hs == "" {
		hs, err := discoverHomeserver(hc, opts.UserID)
		if err != nil {
			return nil, err
		}
		c.hs = hs
	}
	if c.token == "" {
		if err := c.login(opts.Password, opts.DeviceID); err != nil {
			return nil, err
		}
	}
	var who struct {
		UserID string `json:"user_id"`
	}
	if err := c.do(http.MethodGet, "/account/whoami", nil, &who); err != nil {
		return nil, fmt.Errorf("checking matrix credentials: %w", err)
	}
	if who.UserID != opts.UserID {
		return nil, fmt.Errorf("matrix credentials belong to %s, not %s", who.UserID, opts.UserID)
	}
	return c, nil
}

// Homeserver returns the base URL in use.
func (c *Client) Homeserver() string { return c.hs }

// discoverHomeserver follows the client .well-known lookup for the user's
// server name, falling back to https://<server name>.
func discoverHomeserver(hc *http.Client, userID string) (string, error) {
	_, server, ok := strings.Cut(userID, ":")
	if !ok || server == "" {
		return "", fmt.Errorf("cannot derive a homeserver from user ID %q", userID)
	}
	fallback := "https://" + server
	resp, err := hc.Get(fallback + "/.well-known/matrix/client")
	if err != nil {
		return fallback, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallback, nil
	}
	var wk struct {
		Homeserver struct {
			BaseURL string `json:"base_url"`
		} `json:"m.homeserver"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil || wk.Homeserver.BaseURL == "" {
		return fallback, nil
	}
	return strings.TrimRight(wk.Homeserver.BaseURL, "/"), nil
}

func (c *Client) login(password, deviceID string) error {
	req := map[string]any{
		"type":                        "m.login.password",
		"identifier":                  map[string]string{"type": "m.id.user", "user": c.userID},
		"password":                    password,
		"initial_device_display_name": "xmpp-releasetracker",
	}
	if deviceID != "" {
		req["device_id"] = deviceID
	}
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(http.MethodPost, "/login", req, &resp); err != nil {
		return fmt.Errorf("matrix login: %w", err)
	}
	c.token = resp.AccessToken
	c.loggedIn = true
	return nil
}

// JoinRoom joins a room by ID or alias (accepting a pending invite if any)
// and remembers the resolved room ID.
func (c *Client) JoinRoom(roomIDOrAlias string) error {
	_, err := c.resolveRoom(roomIDOrAlias)
	return err
}

func (c *Client) resolveRoom(roomIDOrAlias string) (string, error) {
	c.mu.Lock()
	id, ok := c.rooms[roomIDOrAlias]
	c.mu.Unlock()
	if ok {
		return id, nil
	}
	var resp struct {
		RoomID string `json:"room_id"`
	}
	if err := c.do(http.MethodPost, "/join/"+url.PathEscape(roomIDOrAlias), struct{}{}, &resp); err != nil {
		return "", fmt.Errorf("joining %s: %w", roomIDOrAlias, err)
	}
	c.mu.Lock()
	c.rooms[roomIDOrAlias] = resp.RoomID
	c.mu.Unlock()
	return resp.RoomID, nil
}

// SendRoom posts a notice to a room given by ID or alias, joining it first if
// needed. html is optional (org.matrix.custom.html); plain is the fallback.
func (c *Client) SendRoom(roomIDOrAlias, plain, html string) error {
	roomID, err := c.resolveRoom(roomIDOrAlias)
	if err != nil {
		return err
	}
	err = c.send(roomID, plain, html)
	var merr *Error
	if errors.As(err, &merr) && merr.Code == "M_FORBIDDEN" {
		// Probably kicked since we joined: try joining again once.
		c.mu.Lock()
		delete(c.rooms, roomIDOrAlias)
		c.mu.Unlock()
		if roomID, err = c.resolveRoom(roomIDOrAlias); err != nil {
			return err
		}
		err = c.send(roomID, plain, html)
	}
	return err
}

// SendDirect posts a notice in the bot's direct chat with userID, creating
// and inviting them to one if none exists yet.
func (c *Client) SendDirect(userID, plain, html string) error {
	roomID, err := c.directRoom(userID)
	if err != nil {
		return err
	}
	return c.send(roomID, plain, html)
}

func (c *Client) send(roomID, plain, html string) error {
	content := map[string]string{"msgtype": "m.notice", "body": plain}
	if html != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = html
	}
	// The transaction ID is fixed across retries so the homeserver
	// deduplicates a send whose response was lost.
	txn := fmt.Sprintf("%s-%d", c.txnBase, c.txnSeq.Add(1))
	path := "/rooms/" + url.PathEscape(roomID) + "/send/m.room.message/" + txn
	if err := c.do(http.MethodPut, path, content, nil); err != nil {
		return fmt.Errorf("sending to %s: %w", roomID, err)
	}
	return nil
}

// directRoom finds the DM room with userID: from cache, then from the m.direct
// account data (if the bot is still in it and the user has not left), and
// otherwise by creating a new one and recording it in m.direct.
func (c *Client) directRoom(userID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.directs[userID]; ok {
		return id, nil
	}

	direct, err := c.directMap()
	if err != nil {
		return "", err
	}
	if candidates := direct[userID]; len(candidates) > 0 {
		joined, err := c.joinedRooms()
		if err != nil {
			return "", err
		}
		for i := len(candidates) - 1; i >= 0; i-- { // newest first
			id := candidates[i]
			if joined[id] && c.stillMember(id, userID) {
				c.directs[userID] = id
				return id, nil
			}
		}
	}

	var created struct {
		RoomID string `json:"room_id"`
	}
	req := map[string]any{
		"is_direct": true,
		"preset":    "trusted_private_chat",
		"invite":    []string{userID},
	}
	if err := c.do(http.MethodPost, "/createRoom", req, &created); err != nil {
		return "", fmt.Errorf("creating DM with %s: %w", userID, err)
	}
	direct[userID] = append(direct[userID], created.RoomID)
	if err := c.do(http.MethodPut, c.directPath(), direct, nil); err != nil {
		// Not fatal: the room works, it is just not tagged as a DM, so a
		// restart would create another one.
		log.Printf("Matrix: recording DM with %s in m.direct: %v", userID, err)
	}
	c.directs[userID] = created.RoomID
	return created.RoomID, nil
}

func (c *Client) directPath() string {
	return "/user/" + url.PathEscape(c.userID) + "/account_data/" + directEvent
}

func (c *Client) directMap() (map[string][]string, error) {
	direct := make(map[string][]string)
	err := c.do(http.MethodGet, c.directPath(), nil, &direct)
	var merr *Error
	if errors.As(err, &merr) && merr.Code == "M_NOT_FOUND" {
		return direct, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading m.direct: %w", err)
	}
	return direct, nil
}

func (c *Client) joinedRooms() (map[string]bool, error) {
	var resp struct {
		JoinedRooms []string `json:"joined_rooms"`
	}
	if err := c.do(http.MethodGet, "/joined_rooms", nil, &resp); err != nil {
		return nil, fmt.Errorf("listing joined rooms: %w", err)
	}
	joined := make(map[string]bool, len(resp.JoinedRooms))
	for _, id := range resp.JoinedRooms {
		joined[id] = true
	}
	return joined, nil
}

// stillMember reports whether userID is joined or invited in roomID. On a
// lookup error it assumes yes, rather than spawning a duplicate DM.
func (c *Client) stillMember(roomID, userID string) bool {
	var member struct {
		Membership string `json:"membership"`
	}
	path := "/rooms/" + url.PathEscape(roomID) + "/state/m.room.member/" + url.PathEscape(userID)
	if err := c.do(http.MethodGet, path, nil, &member); err != nil {
		var merr *Error
		return !(errors.As(err, &merr) && merr.Code == "M_NOT_FOUND")
	}
	return member.Membership == "join" || member.Membership == "invite"
}

// Close ends the session if Connect logged in with a password, so restarts
// do not pile up devices on the account. Token sessions are left alone.
func (c *Client) Close() error {
	if !c.loggedIn {
		return nil
	}
	return c.do(http.MethodPost, "/logout", struct{}{}, nil)
}

// do performs a client API call, retrying on rate limiting, 5xx responses and
// network errors. in is JSON-encoded when non-nil; out is decoded when non-nil.
func (c *Client) do(method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		wait, err := c.doOnce(method, path, body, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if wait < 0 || attempt == maxAttempts {
			break
		}
		time.Sleep(wait)
	}
	return lastErr
}

// doOnce returns the error and how long to wait before retrying it, or a
// negative wait when the error is not worth retrying.
func (c *Client) doOnce(method, path string, body []byte, out any) (time.Duration, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.hs+"/_matrix/client/v3"+path, rd)
	if err != nil {
		return -1, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return time.Second, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return time.Second, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return -1, fmt.Errorf("decoding %s response: %w", path, err)
			}
		}
		return 0, nil
	}

	merr := &Error{Status: resp.StatusCode}
	_ = json.Unmarshal(data, merr)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || merr.Code == "M_LIMIT_EXCEEDED":
		wait := time.Duration(merr.RetryAfterMS) * time.Millisecond
		if s := resp.Header.Get("Retry-After"); wait == 0 && s != "" {
			if d, err := time.ParseDuration(s + "s"); err == nil {
				wait = d
			}
		}
		if wait <= 0 {
			wait = time.Second
		}
		return min(wait, maxRetryAfter), merr
	case resp.StatusCode >= 500:
		return 2 * time.Second, merr
	}
	return -1, merr
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
