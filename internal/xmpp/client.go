package xmpp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"log"
	"strings"

	goxmpp "github.com/xmppo/go-xmpp"
)

// Client wraps a go-xmpp connection.
type Client struct {
	conn    *goxmpp.Client
	mucNick string
}

// Connect establishes an XMPP connection using the given credentials.
func Connect(jid, password, server, mucNick string) (*Client, error) {
	opts := goxmpp.Options{
		Host:                         server,
		User:                         jid,
		Password:                     password,
		StartTLS:                     true,
		InsecureAllowUnencryptedAuth: false,
		NoTLS:                        false,
		Debug:                        false,
		Session:                      false,
		Status:                       "available",
		StatusMessage:                "",
	}

	conn, err := opts.NewClient()
	if err != nil {
		return nil, fmt.Errorf("connecting to XMPP server: %w", err)
	}

	return &Client{conn: conn, mucNick: mucNick}, nil
}

// JoinMUC sends an initial presence to the given MUC room JID.
func (c *Client) JoinMUC(roomJID string) error {
	fullJID := roomJID + "/" + c.mucNick
	_, err := c.conn.SendPresence(goxmpp.Presence{To: fullJID})
	if err != nil {
		return fmt.Errorf("joining MUC %s: %w", roomJID, err)
	}
	return nil
}

// SendMUC sends a groupchat message to the given MUC room JID.
// If html is non-empty, an XHTML-IM body is included alongside the plain text fallback.
func (c *Client) SendMUC(roomJID, plain, html string) error {
	return c.sendMessage(roomJID, "groupchat", plain, html)
}

// SendDirect sends a direct (chat) message to the given JID.
// If html is non-empty, an XHTML-IM body is included alongside the plain text fallback.
func (c *Client) SendDirect(jid, plain, html string) error {
	return c.sendMessage(jid, "chat", plain, html)
}

// sendMessage sends a message, using a raw XHTML-IM stanza when html is provided.
func (c *Client) sendMessage(to, msgType, plain, html string) error {
	if html == "" {
		_, err := c.conn.Send(goxmpp.Chat{
			Remote: to,
			Type:   msgType,
			Text:   plain,
		})
		return err
	}

	// Build a stanza with both a plain-text <body> fallback and an XHTML-IM <html> block.
	// Clients that don't support XEP-0071 will display the plain text body.
	stanza := fmt.Sprintf(
		"<message to='%s' type='%s' xml:lang='en' id='%s'>"+
			"<body>%s</body>"+
			"<html xmlns='http://jabber.org/protocol/xhtml-im'>"+
			"<body xmlns='http://www.w3.org/1999/xhtml'>%s</body>"+
			"</html>"+
			"</message>",
		xmlEscape(to),
		xmlEscape(msgType),
		randomID(),
		xmlEscape(plain),
		html, // already-built HTML; content must be valid XHTML
	)
	_, err := c.conn.SendOrg(stanza)
	if err != nil {
		return fmt.Errorf("sending HTML message to %s: %w", to, err)
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
				if !strings.Contains(err.Error(), "use of closed network connection") {
					log.Printf("XMPP recv error: %v", err)
				}
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
