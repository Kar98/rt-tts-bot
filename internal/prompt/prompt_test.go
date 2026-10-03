package prompt

import (
	"testing"

	assert "github.com/stretchr/testify/assert"
)

func TestRender(t *testing.T) {
	got := Render("call <a> then <b>, keep {state?} and <unknown>", map[string]string{
		"a": "tool_a",
		"b": "tool_b",
	})
	assert.Equal(t, "call tool_a then tool_b, keep {state?} and <unknown>", got)
}
