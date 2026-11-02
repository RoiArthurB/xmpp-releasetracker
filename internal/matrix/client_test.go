package matrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const botID = "@bot:example.org"

// fakeHS is a tiny in-memory homeserver covering the endpoints the client uses.
type fakeHS struct {
	t  *testing.T
	mu sync.Mutex

	token       string
	direct      map[string][]string // m.direct account data; nil means unset
	joined      map[string]bool
	members     map[string]string // roomID|userID → membership
	aliases     map[string]string // alias → room ID
	sent        []sentEvent
	txns        map[string]bool
	created     int
	rateLimitN  int // number of upcoming sends to answer with 429
	forbiddenN  int // number of upcoming sends to answer with M_FORBIDDEN
	loggedOut   bool
	loginDevice string
}

type sentEvent struct {
	room    string
	content map[string]string
}

func newFakeHS(t *testing.T) (*fakeHS, *httptest.Server) {
	f := &fakeHS{
		t:       t,
		token:   "tok",
		joined:  map[string]bool{},
		members: map[string]string{},
		aliases: map[string]string{"#releases:example.org": "!rel:example.org"},
		txns:    map[string]bool{},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeHS) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Path is percent-decoded, as a real server sees IDs and aliases.
	path := strings.TrimPrefix(r.URL.Path, "/_matrix/client/v3")
	if strings.Contains(r.URL.RawPath, "#") {
		f.t.Errorf("unescaped # in request path %q", r.URL.RawPath)
	}
	if path == "/login" {
		var req struct {
			Password string `json:"password"`
			DeviceID string `json:"device_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Password != "pw" {
			writeJSON(w, 403, map[string]string{"errcode": "M_FORBIDDEN", "error": "bad password"})
			return
		}
		f.loginDevice = req.DeviceID
		writeJSON(w, 200, map[string]string{"access_token": f.token, "user_id": botID})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		writeJSON(w, 401, map[string]string{"errcode": "M_UNKNOWN_TOKEN", "error": "bad token"})
		return
	}

	switch {
	case path == "/account/whoami":
		writeJSON(w, 200, map[string]string{"user_id": botID})
	case path == "/logout":
		f.loggedOut = true
		writeJSON(w, 200, struct{}{})
	case strings.HasPrefix(path, "/join/"):
		target := strings.TrimPrefix(path, "/join/")
		id := target
		if strings.HasPrefix(target, "#") {
			var ok bool
			if id, ok = f.aliases[target]; !ok {
				writeJSON(w, 404, map[string]string{"errcode": "M_NOT_FOUND", "error": "no alias"})
				return
			}
		}
		f.joined[id] = true
		writeJSON(w, 200, map[string]string{"room_id": id})
	case path == "/joined_rooms":
		var ids []string
		for id := range f.joined {
			ids = append(ids, id)
		}
		writeJSON(w, 200, map[string][]string{"joined_rooms": ids})
	case path == "/user/"+botID+"/account_data/m.direct":
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&f.direct)
			writeJSON(w, 200, struct{}{})
			return
		}
		if f.direct == nil {
			writeJSON(w, 404, map[string]string{"errcode": "M_NOT_FOUND", "error": "no data"})
			return
		}
		writeJSON(w, 200, f.direct)
	case path == "/createRoom":
		var req struct {
			IsDirect bool     `json:"is_direct"`
			Invite   []string `json:"invite"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !req.IsDirect || len(req.Invite) != 1 {
			f.t.Errorf("createRoom request not a DM: %+v", req)
		}
		f.created++
		id := "!dm" + string(rune('0'+f.created)) + ":example.org"
		f.joined[id] = true
		f.members[id+"|"+req.Invite[0]] = "invite"
		writeJSON(w, 200, map[string]string{"room_id": id})
	case strings.HasPrefix(path, "/rooms/") && strings.Contains(path, "/state/m.room.member/"):
		rest := strings.TrimPrefix(path, "/rooms/")
		room, user, _ := strings.Cut(rest, "/state/m.room.member/")
		m, ok := f.members[room+"|"+user]
		if !ok {
			writeJSON(w, 404, map[string]string{"errcode": "M_NOT_FOUND", "error": "no member"})
			return
		}
		writeJSON(w, 200, map[string]string{"membership": m})
	case strings.HasPrefix(path, "/rooms/") && strings.Contains(path, "/send/m.room.message/"):
		rest := strings.TrimPrefix(path, "/rooms/")
		room, txn, _ := strings.Cut(rest, "/send/m.room.message/")
		if f.rateLimitN > 0 {
			f.rateLimitN--
			writeJSON(w, 429, map[string]any{"errcode": "M_LIMIT_EXCEEDED", "error": "slow down", "retry_after_ms": 1})
			return
		}
		if f.forbiddenN > 0 {
			f.forbiddenN--
			writeJSON(w, 403, map[string]string{"errcode": "M_FORBIDDEN", "error": "not in room"})
			return
		}
		if !f.joined[room] {
			writeJSON(w, 403, map[string]string{"errcode": "M_FORBIDDEN", "error": "not in room"})
			return
		}
		if f.txns[txn] {
			f.t.Errorf("transaction ID %s reused for a distinct message", txn)
		}
		f.txns[txn] = true
		var content map[string]string
		_ = json.NewDecoder(r.Body).Decode(&content)
		f.sent = append(f.sent, sentEvent{room, content})
		writeJSON(w, 200, map[string]string{"event_id": "$" + txn})
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, path)
		writeJSON(w, 404, map[string]string{"errcode": "M_UNRECOGNIZED", "error": "unknown"})
	}
}

func connect(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := Connect(Options{Homeserver: srv.URL, UserID: botID, AccessToken: "tok"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

func TestConnectRejectsWrongUser(t *testing.T) {
	_, srv := newFakeHS(t)
	if _, err := Connect(Options{Homeserver: srv.URL, UserID: "@other:example.org", AccessToken: "tok"}); err == nil {
		t.Fatal("Connect accepted a token belonging to another user")
	}
}

func TestConnectBadToken(t *testing.T) {
	_, srv := newFakeHS(t)
	_, err := Connect(Options{Homeserver: srv.URL, UserID: botID, AccessToken: "nope"})
	if err == nil || !strings.Contains(err.Error(), "M_UNKNOWN_TOKEN") {
		t.Fatalf("err = %v, want M_UNKNOWN_TOKEN", err)
	}
}

func TestPasswordLoginLogsOutOnClose(t *testing.T) {
	f, srv := newFakeHS(t)
	c, err := Connect(Options{Homeserver: srv.URL, UserID: botID, Password: "pw", DeviceID: "BOTDEVICE"})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if f.loginDevice != "BOTDEVICE" {
		t.Errorf("login device = %q, want the configured device_id", f.loginDevice)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !f.loggedOut {
		t.Error("password session was not logged out on Close")
	}
}

func TestTokenSessionNotLoggedOut(t *testing.T) {
	f, srv := newFakeHS(t)
	if err := connect(t, srv).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if f.loggedOut {
		t.Error("Close logged out a user-provided access token")
	}
}

func TestSendRoomByAlias(t *testing.T) {
	f, srv := newFakeHS(t)
	c := connect(t, srv)
	if err := c.JoinRoom("#releases:example.org"); err != nil {
		t.Fatalf("JoinRoom: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := c.SendRoom("#releases:example.org", "plain", "<b>html</b>"); err != nil {
			t.Fatalf("SendRoom: %v", err)
		}
	}
	if len(f.sent) != 2 || f.sent[0].room != "!rel:example.org" {
		t.Fatalf("sent = %+v, want 2 events in !rel:example.org", f.sent)
	}
	got := f.sent[0].content
	if got["msgtype"] != "m.notice" || got["body"] != "plain" ||
		got["format"] != "org.matrix.custom.html" || got["formatted_body"] != "<b>html</b>" {
		t.Errorf("content = %+v", got)
	}
}

func TestSendRoomRetriesRateLimit(t *testing.T) {
	f, srv := newFakeHS(t)
	c := connect(t, srv)
	f.rateLimitN = 2
	if err := c.SendRoom("!rel:example.org", "plain", ""); err != nil {
		t.Fatalf("SendRoom: %v", err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent %d events, want 1", len(f.sent))
	}
	if _, ok := f.sent[0].content["format"]; ok {
		t.Error("format set without an HTML body")
	}
}

func TestSendRoomRejoinsAfterForbidden(t *testing.T) {
	f, srv := newFakeHS(t)
	c := connect(t, srv)
	if err := c.JoinRoom("!rel:example.org"); err != nil {
		t.Fatalf("JoinRoom: %v", err)
	}
	f.forbiddenN = 1 // e.g. kicked since startup
	if err := c.SendRoom("!rel:example.org", "plain", ""); err != nil {
		t.Fatalf("SendRoom: %v", err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent %d events, want 1", len(f.sent))
	}
}

func TestSendDirectCreatesDMOnceAndRecordsIt(t *testing.T) {
	f, srv := newFakeHS(t)
	c := connect(t, srv)
	const alice = "@alice:example.org"
	for i := 0; i < 2; i++ {
		if err := c.SendDirect(alice, "hi", ""); err != nil {
			t.Fatalf("SendDirect: %v", err)
		}
	}
	if f.created != 1 {
		t.Fatalf("created %d rooms, want 1", f.created)
	}
	if got := f.direct[alice]; len(got) != 1 || got[0] != "!dm1:example.org" {
		t.Errorf("m.direct = %+v, want alice → !dm1", f.direct)
	}

	// A fresh client (bot restart) reuses the DM recorded in m.direct.
	if err := connect(t, srv).SendDirect(alice, "again", ""); err != nil {
		t.Fatalf("SendDirect after restart: %v", err)
	}
	if f.created != 1 {
		t.Errorf("restart created a new DM instead of reusing m.direct")
	}
	if len(f.sent) != 3 || f.sent[2].room != "!dm1:example.org" {
		t.Errorf("sent = %+v", f.sent)
	}
}

func TestSendDirectReplacesDMTheUserLeft(t *testing.T) {
	f, srv := newFakeHS(t)
	const alice = "@alice:example.org"
	f.direct = map[string][]string{alice: {"!old:example.org"}, "@bob:example.org": {"!bob:example.org"}}
	f.joined["!old:example.org"] = true
	f.members["!old:example.org|"+alice] = "leave"

	if err := connect(t, srv).SendDirect(alice, "hi", ""); err != nil {
		t.Fatalf("SendDirect: %v", err)
	}
	if f.created != 1 || f.sent[0].room != "!dm1:example.org" {
		t.Fatalf("want a new DM since alice left the old one; created=%d sent=%+v", f.created, f.sent)
	}
	if len(f.direct["@bob:example.org"]) != 1 {
		t.Errorf("updating m.direct dropped other users' entries: %+v", f.direct)
	}
}

func TestDiscoverHomeserver(t *testing.T) {
	// The discovery fallback (no .well-known) is https://<server>.
	hs, err := discoverHomeserver(&http.Client{Transport: failingTransport{}}, "@bot:example.org")
	if err != nil || hs != "https://example.org" {
		t.Errorf("discoverHomeserver = %q, %v; want https://example.org", hs, err)
	}
	if _, err := discoverHomeserver(http.DefaultClient, "bot"); err == nil {
		t.Error("discoverHomeserver accepted a user ID without a server")
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrHandlerTimeout
}
