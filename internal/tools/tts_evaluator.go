package tools

import (
	"errors"
	"fmt"
	"log/slog"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// TTSEvaluatorArgs is empty. The model never sees the chat messages, since
// read_twitch_chat only returns a count, so the tool reads them from session
// state instead of asking the model for them.
type TTSEvaluatorArgs struct{}

type TTSEvaluatorResult struct {
	Worthy bool   `json:"worthy"`
	Error  string `json:"error,omitempty"`
}

const TTSEvaluatorToolname = "tts_evaluator"

func NewTTSEvaluatorTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: TTSEvaluatorToolname,
		Description: "The purpose of this tool is to determine if it's worth going ahead with creating a donation message based on the content " +
			"of the messages saved by " + ReadChatToolName + ". Takes no arguments.",
	}, func(ctx agent.Context, _ TTSEvaluatorArgs) (TTSEvaluatorResult, error) {
		return evaluate(ctx.State()), nil
	})
}

// evaluate runs isWorthy over the messages read_twitch_chat saved in state.
func evaluate(state session.State) TTSEvaluatorResult {
	messages, err := savedMessages(state)
	if err != nil {
		slog.Error("tts_evaluator: failed", "err", err)
		return TTSEvaluatorResult{Error: err.Error()}
	}
	return isWorthy(messages)
}

// savedMessages returns the message texts under MessagesStateKey. A session
// service that stores state as JSON gives back []any rather than []string.
func savedMessages(state session.State) ([]string, error) {
	v, err := state.Get(MessagesStateKey)
	if errors.Is(err, session.ErrStateKeyNotExist) {
		return nil, fmt.Errorf("no chat messages saved, call %s first", ReadChatToolName)
	}
	if err != nil {
		return nil, err
	}
	switch v := v.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, len(v))
		for i, m := range v {
			s, ok := m.(string)
			if !ok {
				return nil, fmt.Errorf("state %q: element %d is %T, want string", MessagesStateKey, i, m)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("state %q is %T, want []string", MessagesStateKey, v)
	}
}

func isWorthy(messages []string) TTSEvaluatorResult {
	res := TTSEvaluatorResult{}
	if len(messages) > 10 {
		res.Worthy = true
	}
	slog.Info("isWorthy()", "messages", len(messages), "res.Worthy", res.Worthy)
	return res
}
