package main

import (
	"testing"

	assert "github.com/stretchr/testify/assert"

	"github.com/Kar98/artosis-tts-agent/internal/prompt"
)

func TestInstructionPlaceholdersFilled(t *testing.T) {
	instructions := prompt.Render(instructionTemplate, instructionVars)
	assert.NotContains(t, instructions, "<")
	assert.NotContains(t, instructions, ">")
	assert.NotContains(t, instructions, "{")
	assert.NotContains(t, instructions, "}")
}
