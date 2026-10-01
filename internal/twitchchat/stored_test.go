package twitchchat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageLoading(t *testing.T) {
	ss := StoredSource{}
	messages, err := ss.Fetch(t.Context(), Request{Channel: "random_chat.txt"})
	assert.NoError(t, err)
	assert.NotEmpty(t, messages)
	// Check empty
	messages, err = ss.Fetch(t.Context(), Request{Channel: "empty.txt"})
	assert.NoError(t, err)
	assert.Empty(t, messages)
}

func TestMessageLoadingMissingFile(t *testing.T) {
	_, err := StoredSource{}.Fetch(t.Context(), Request{Channel: "missing.txt"})
	assert.Error(t, err)
	_, err = StoredSource{}.Fetch(t.Context(), Request{Channel: "../stored.go"})
	assert.Error(t, err)
}
