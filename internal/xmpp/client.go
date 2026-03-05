package xmpp

import (
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
		Host:     server,
		User:     jid,
		Password: password,
		// Use StartTLS when available; fall back to plain if required
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

	c := &Client{conn: conn, mucNick: mucNick}
	return c, nil
}

// JoinMUC sends an initial presence to the given MUC room JID.
func (c *Client) JoinMUC(roomJID string) error {
	// Send presence to room@server/nick
	fullJID := roomJID + "/" + c.mucNick
	_, err := c.conn.SendPresence(goxmpp.Presence{To: fullJID})
	if err != nil {
		return fmt.Errorf("joining MUC %s: %w", roomJID, err)
	}
	return nil
}

// SendMUC sends a groupchat message to the given MUC room JID.
func (c *Client) SendMUC(roomJID, message string) error {
	_, err := c.conn.Send(goxmpp.Chat{
		Remote: roomJID,
		Type:   "groupchat",
		Text:   message,
	})
	if err != nil {
		return fmt.Errorf("sending MUC message to %s: %w", roomJID, err)
	}
	return nil
}

// SendDirect sends a direct (chat) message to the given JID.
func (c *Client) SendDirect(jid, message string) error {
	_, err := c.conn.Send(goxmpp.Chat{
		Remote: jid,
		Type:   "chat",
		Text:   message,
	})
	if err != nil {
		return fmt.Errorf("sending direct message to %s: %w", jid, err)
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
