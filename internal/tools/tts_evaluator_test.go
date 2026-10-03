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
