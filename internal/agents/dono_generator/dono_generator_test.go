package donogenerator

import (
	"strings"
	"testing"

	"github.com/Kar98/artosis-tts-agent/internal/prompt"
)

func TestInstructionPlaceholdersFilled(t *testing.T) {
	got := prompt.Render(instructionTemplate, instructionVars)
	if strings.ContainsAny(got, "<>") {
		t.Errorf("instruction has unfilled <placeholder>:\n%s", got)
	}
}
