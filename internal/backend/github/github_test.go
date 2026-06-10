package github

import "testing"

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "hello world", "hello world"},
		{"tags removed", "<p>hello <b>world</b></p>", "hello world"},
		{"entities decoded", "a &amp; b &lt;c&gt; &quot;d&quot; &#39;e&#39;", `a & b <c> "d" 'e'`},
		// A literal "&lt;" written by the author arrives double-encoded as
		// "&amp;lt;" and must decode to "&lt;", not "<".
		{"no double decoding", "use &amp;lt; for less-than", "use &lt; for less-than"},
		{"named entities beyond the basic five", "caf&eacute; &mdash; ok", "café — ok"},
		{"whitespace trimmed", "  <p>x</p>  ", "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripHTML(tt.in); got != tt.want {
				t.Errorf("stripHTML(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
