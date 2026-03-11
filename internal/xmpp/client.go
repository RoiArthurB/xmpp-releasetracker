package xmpp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"strings"
	"time"

	goxmpp "github.com/xmppo/go-xmpp"
)

const keepAliveInterval = 30 * time.Second

// Client wraps a go-xmpp connection.
type Client struct {
	conn     *goxmpp.Client
	mucNick  string
	jid      string
	password string
	server   string
	rooms    []string
}

// Connect establishes an XMPP connection using the given credentials.
func Connect(jid, password, server, mucNick string) (*Client, error) {
	c := &Client{mucNick: mucNick, jid: jid, password: password, server: server}
	if err := c.Reconnect(); err != nil {
		return nil, err
	}
	return c, nil
}

// Reconnect drops the current connection (if any) and re-establishes it,
// then restarts background goroutines and rejoins all MUC rooms.
func (c *Client) Reconnect() error {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	conn, err := goxmpp.Options{
		Host:                         c.server,
		User:                         c.jid,
		Password:                     c.password,
		StartTLS:                     true,
		InsecureAllowUnencryptedAuth: false,
		NoTLS:                        false,
		Debug:                        false,
		Session:                      false,
		Status:                       "available",
		StatusMessage:                "",
	}.NewClient()
	if err != nil {
		return fmt.Errorf("connecting to XMPP server: %w", err)
	}
	c.conn = conn
	c.DiscardIncoming()
	c.SendKeepAlives()
	for _, room := range c.rooms {
		if _, err := c.conn.SendPresence(goxmpp.Presence{To: room + "/" + c.mucNick}); err != nil {
			log.Printf("Rejoining MUC %s after reconnect: %v", room, err)
		}
	}
	return nil
}

// JoinMUC sends an initial presence to the given MUC room JID and remembers
// the room so it can be rejoined automatically after a reconnect.
func (c *Client) JoinMUC(roomJID string) error {
	fullJID := roomJID + "/" + c.mucNick
	_, err := c.conn.SendPresence(goxmpp.Presence{To: fullJID})
	if err != nil {
		return fmt.Errorf("joining MUC %s: %w", roomJID, err)
	}
	c.rooms = append(c.rooms, roomJID)
	return nil
}

// SendMUC sends a groupchat message to the given MUC room JID.
// avatarURL, when non-empty, must be the first line of body; a XEP-0385 SIMS
// reference is added so supporting clients render it as an inline image.
func (c *Client) SendMUC(roomJID, body, avatarURL string) error {
	return c.sendMessage(roomJID, "groupchat", body, avatarURL)
}

// SendDirect sends a direct (chat) message to the given JID.
func (c *Client) SendDirect(jid, body, avatarURL string) error {
	return c.sendMessage(jid, "chat", body, avatarURL)
}

// sendMessage builds and sends a raw XMPP stanza containing:
//   - a plain-text <body> (text includes XEP-0393 styling markers)
//   - an optional XEP-0385 SIMS <reference> when avatarURL is non-empty
//   - a XEP-0393 <styling> hint element
func (c *Client) sendMessage(to, msgType, body, avatarURL string) error {
	var extras strings.Builder

	// XEP-0385 (Stateless Inline Media Sharing): reference for the avatar image.
	// The avatar URL must appear at the very start of the body (begin=0);
	// end is the number of Unicode code points in the URL.
	if avatarURL != "" {
		end := len([]rune(avatarURL))
		fmt.Fprintf(&extras,
			"<reference xmlns='urn:xmpp:reference:0' begin='0' end='%d' type='data' uri='%s'>"+
				"<media-sharing xmlns='urn:xmpp:sims:1'>"+
				"<file xmlns='urn:xmpp:jingle:apps:file-transfer:5'>"+
				"<media-type>%s</media-type>"+
				"</file>"+
				"<sources>"+
				"<reference xmlns='urn:xmpp:reference:0' type='data' uri='%s'/>"+
				"</sources>"+
				"</media-sharing>"+
				"</reference>",
			end,
			xmlEscape(avatarURL),
			xmlEscape(mimeTypeFromURL(avatarURL)),
			xmlEscape(avatarURL),
		)
	}

	// XEP-0393 (Message Styling): hint so clients know the body uses styling markers.
	extras.WriteString("<styling xmlns='urn:xmpp:styling:0'/>")

	stanza := fmt.Sprintf(
		"<message to='%s' type='%s' xml:lang='en' id='%s'>"+
			"<body>%s</body>"+
			"%s"+
			"</message>",
		xmlEscape(to),
		xmlEscape(msgType),
		randomID(),
		xmlEscape(body),
		extras.String(),
	)
	_, err := c.conn.SendOrg(stanza)
	if err != nil {
		log.Printf("XMPP send error (%v), reconnecting and retrying...", err)
		if rerr := c.Reconnect(); rerr != nil {
			return fmt.Errorf("sending message to %s: %w (reconnect failed: %v)", to, err, rerr)
		}
		if _, err = c.conn.SendOrg(stanza); err != nil {
			return fmt.Errorf("sending message to %s: %w", to, err)
		}
	}
	return nil
}

// DiscardIncoming starts a goroutine that discards all incoming stanzas.
// This is required to keep the connection alive.
func (c *Client) DiscardIncoming() {
	go func() {
		for {
			_, err := c.conn.Recv()
			if err != nil {
				if !strings.Contains(err.Error(), "use of closed network connection") && err.Error() != "EOF" {
					log.Printf("XMPP recv error: %v", err)
				}
				return
			}
		}
	}()
}

// SendKeepAlives starts a goroutine that periodically sends a whitespace
// keep-alive to prevent the server from closing an idle connection during
// long poll cycles.
func (c *Client) SendKeepAlives() {
	go func() {
		ticker := time.NewTicker(keepAliveInterval)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := c.conn.SendKeepAlive(); err != nil {
				log.Printf("XMPP keep-alive error: %v", err)
				return
			}
		}
	}()
}

// Close disconnects from the XMPP server.
func (c *Client) Close() error {
	return c.conn.Close()
}

// xmlEscape escapes s for use in XML text content or attribute values.
func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// randomID returns a random hex string suitable for use as a message ID.
func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mimeTypeFromURL guesses the image MIME type from the URL extension.
// Defaults to image/png, which covers GitHub .png avatar URLs and most forge APIs.
func mimeTypeFromURL(u string) string {
	lower := strings.ToLower(u)
	switch {
	case strings.Contains(lower, ".jpg"), strings.Contains(lower, ".jpeg"):
		return "image/jpeg"
	case strings.Contains(lower, ".gif"):
		return "image/gif"
	case strings.Contains(lower, ".webp"):
		return "image/webp"
	default:
		return "image/png"
	}
}
