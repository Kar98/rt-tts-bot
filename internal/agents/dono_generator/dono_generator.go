package donogenerator

import (
	_ "embed"
	"log/slog"

	"github.com/Kar98/artosis-tts-agent/internal/prompt"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
)

//go:embed instruction.md
var instruction string

var instructionVars = map[string]string{
	"channelType": "starcraft brood war channel",
}

const AgentName = "dono_generator"

func NewSummariser(m model.LLM) (agent.Agent, error) {
	agent_instruction := prompt.Render(instruction, instructionVars)
	slog.Info("new dono generator")
	return llmagent.New(llmagent.Config{
		Name:        AgentName,
		Model:       m,
		Description: "This will generate a donation message based on the messages passed to it",
		Instruction: agent_instruction,
	})
}
