package xmpp

import (
	"strings"
	"testing"
)

func TestXMLEscape(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"a & b", "a &amp; b"},
		{"<body>", "&lt;body&gt;"},
		// Both quote styles must be escaped: the stanza builder puts
		// attribute values in single quotes.
		{`it's "quoted"`, "it&#39;s &#34;quoted&#34;"},
	}
	for _, tt := range tests {
		if got := xmlEscape(tt.in); got != tt.want {
			t.Errorf("xmlEscape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestXMLEscapeNeutralizesInjection(t *testing.T) {
	// A malicious release note must not be able to break out of <body>
	// and inject stanzas.
	payload := `</body></message><message to='victim@example.org'><body>pwn</body>`
	got := xmlEscape(payload)
	if strings.ContainsAny(got, "<>") {
		t.Errorf("escaped payload still contains angle brackets: %q", got)
	}
}

func TestMimeTypeFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.org/a.png", "image/png"},
		{"https://example.org/a.jpg", "image/jpeg"},
		{"https://example.org/a.JPEG", "image/jpeg"},
		{"https://example.org/a.gif", "image/gif"},
		{"https://example.org/a.webp", "image/webp"},
		{"https://github.com/owner.png?size=400", "image/png"},
		{"https://example.org/avatar", "image/png"}, // no extension: default
	}
	for _, tt := range tests {
		if got := mimeTypeFromURL(tt.url); got != tt.want {
			t.Errorf("mimeTypeFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestRandomID(t *testing.T) {
	a, b := randomID(), randomID()
	if len(a) != 16 {
		t.Errorf("randomID length = %d, want 16 hex chars", len(a))
	}
	if a == b {
		t.Error("two randomID calls returned the same value")
	}
}
