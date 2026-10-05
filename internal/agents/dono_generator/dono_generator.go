package donogenerator

import (
	_ "embed"
	"log/slog"

	"github.com/Kar98/artosis-tts-agent/internal/prompt"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

//go:embed instruction.md
var instructionTemplate string
var instructionVars = map[string]string{
	"channelType": "starcraft brood war channel",
}

const AgentName = "dono_generator"
const LastDonoMessage = "last_dono_message"

func NewSummariser(m model.LLM) (agent.Agent, error) {

	agent_instruction := prompt.Render(instructionTemplate, instructionVars)
	slog.Info("new dono generator")
	return llmagent.New(llmagent.Config{
		Name:        AgentName,
		Model:       m,
		Description: "This will generate a donation message based on the messages passed to it",
		Instruction: agent_instruction,
		OutputKey:   LastDonoMessage,
		AfterAgentCallbacks: []agent.AfterAgentCallback{func(ctx agent.Context) (*genai.Content, error) {
			var lastMessage any
			lastMessage, err := ctx.State().Get(LastDonoMessage)
			slog.Info("AfterAgentCallback-dono_generator", LastDonoMessage, lastMessage)
			return nil, err
		}},
	})
}
