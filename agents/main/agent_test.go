package main

import (
	"testing"

	assert "github.com/stretchr/testify/assert"
)

func TestInstructionLoading(t *testing.T) {
	mappings := map[string]string{"tools.ReadChatToolName": "UNIQUE_111", "agents.SummariserName": "UNIQUE_222"}
	instructions, err := loadInstruction(mappings)
	assert.NoError(t, err)
	assert.Contains(t, instructions, "UNIQUE_111")
	assert.Contains(t, instructions, "UNIQUE_222")
	assert.NotContains(t, instructions, "{")
	assert.NotContains(t, instructions, "}")

}
