package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTTSEvalCreator(t *testing.T) {
	tool, err := NewTTSEvaluatorTool()
	assert.NoError(t, err)
	assert.NotEmpty(t, tool)
}

func TestTTSEvalFunc(t *testing.T) {
	under := make([]string, 9)
	underRes := isWorthy(under)
	assert.False(t, underRes.Worthy)
	boundary := make([]string, 10)
	boundaryRes := isWorthy(boundary)
	assert.False(t, boundaryRes.Worthy)
	over := make([]string, 11)
	overRes := isWorthy(over)
	assert.True(t, overRes.Worthy)
}

func TestTTSEvalReadsState(t *testing.T) {
	res := evaluate(mapState{MessagesStateKey: make([]string, 11)})
	assert.Empty(t, res.Error)
	assert.True(t, res.Worthy)
}

// A session service that stores state as JSON hands back []any.
func TestTTSEvalReadsJSONState(t *testing.T) {
	msgs := make([]any, 11)
	for i := range msgs {
		msgs[i] = "gg"
	}
	res := evaluate(mapState{MessagesStateKey: msgs})
	assert.Empty(t, res.Error)
	assert.True(t, res.Worthy)
}

func TestTTSEvalNoMessages(t *testing.T) {
	res := evaluate(mapState{})
	assert.Contains(t, res.Error, ReadChatToolName)
	assert.False(t, res.Worthy)
}
