// Package agents builds the LLM agents used by the Twitch chat agent.
package summariser

import (
	_ "embed"
	"log"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"

	"github.com/Kar98/artosis-tts-agent/internal/prompt"
	"github.com/Kar98/artosis-tts-agent/internal/tools"
)

// SummariserName is the agent name, and so the tool name the main agent calls.
const SummariserName = "chat_summariser"

// MaxSummaryRunes is the hard limit on the summary length.
const MaxSummaryRunes = 300

//go:embed instruction.md
var instructionTemplate string

// instructionVars fills the <placeholders> in instruction.md. The rendered
// {chat_transcript?} is left for ADK to fill from session state.
var instructionVars = map[string]string{
	"tools.TranscriptStateKey": tools.TranscriptStateKey,
}

// NewSummariser returns the chat_summariser agent. It reads the transcript that
// read_twitch_chat stored in session state, and its output is truncated to
// MaxSummaryRunes even if the model ignores the instruction.
func NewSummariser(m model.LLM) (agent.Agent, error) {
	return llmagent.New(llmagent.Config{
		Name:        SummariserName,
		Model:       m,
		Description: "Summarises the Twitch chat saved by read_twitch_chat in at most 300 characters.",
		Instruction: prompt.Render(instructionTemplate, instructionVars),
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			func(ctx agent.Context, llmRequest *model.LLMRequest) (*model.LLMResponse, error) {
				if llmRequest == nil {
					return nil, nil
				}
				// example: FormatTranscript renders messages one per line as "[15:04:05] user: text".
				// type = string
				transcript, err := ctx.State().Get(tools.TranscriptStateKey)
				if err != nil {
					return nil, err
				}
				if len(transcript.(string)) == 0 {
					log.Printf("%s", transcript)
					return &model.LLMResponse{
						Content: genai.NewContentFromText("No chat messages were posted", genai.RoleModel),
					}, nil
				}
				return nil, nil
			},
		},
		AfterModelCallbacks: []llmagent.AfterModelCallback{
			func(_ agent.Context, resp *model.LLMResponse, err error) (*model.LLMResponse, error) {
				if err != nil || resp == nil {
					return nil, nil
				}
				limitText(resp.Content, MaxSummaryRunes)
				return resp, nil
			},
		},
	})
}

// limitText joins the non-thought text parts of c and truncates them to n
// runes, replacing them with a single text part. Other parts are kept.
func limitText(c *genai.Content, n int) {
	if c == nil {
		return
	}
	var (
		text  strings.Builder
		parts []*genai.Part
		first = -1
	)
	for _, p := range c.Parts {
		if p == nil || p.Text == "" || p.Thought {
			parts = append(parts, p)
			continue
		}
		if first < 0 {
			first = len(parts)
			parts = append(parts, &genai.Part{})
		}
		text.WriteString(p.Text)
	}
	if first < 0 {
		return
	}
	parts[first].Text = truncateRunes(strings.TrimSpace(text.String()), n)
	c.Parts = parts
}

// truncateRunes returns s cut to at most n runes.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
