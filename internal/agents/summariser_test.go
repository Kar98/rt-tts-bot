package agents

import (
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/genai"
)

func TestTruncateRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 3, "hel"},
		{"hello", 0, ""},
		{"", 3, ""},
		{"héllo wörld", 4, "héll"},
		{"🎮🔥👀gg", 2, "🎮🔥"},
		{"日本語テキスト", 3, "日本語"},
	} {
		if got := truncateRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestLimitText(t *testing.T) {
	c := &genai.Content{Parts: []*genai.Part{
		{Text: "thinking...", Thought: true},
		{Text: strings.Repeat("🔥", 200)},
		{Text: strings.Repeat("a", 200)},
	}}
	limitText(c, MaxSummaryRunes)
	if len(c.Parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(c.Parts))
	}
	if !c.Parts[0].Thought {
		t.Errorf("thought part should be kept first")
	}
	got := c.Parts[1].Text
	if n := utf8.RuneCountInString(got); n != MaxSummaryRunes {
		t.Errorf("summary has %d runes, want %d", n, MaxSummaryRunes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("summary is not valid UTF-8")
	}
}

func TestLimitTextNoText(t *testing.T) {
	limitText(nil, 10)
	c := &genai.Content{Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "f"}}}}
	limitText(c, 10)
	if len(c.Parts) != 1 || c.Parts[0].FunctionCall == nil {
		t.Errorf("non-text parts should be unchanged: %+v", c.Parts)
	}
}
