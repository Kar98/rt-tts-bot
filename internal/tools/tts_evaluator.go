package tools

import (
	"log/slog"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

type TTSEvaluatorArgs struct {
	Messages []string `json:"messages" jsonschema:"contains a list of twitch messages with only the user's text."`
}

type TTSEvaluatorResult struct {
	Worthy bool   `json:"worthy"`
	Error  string `json:"error,omitempty"`
}

const TTSEvaluatorToolname = "tts_evaluator"

func NewTTSEvaluatorTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: TTSEvaluatorToolname,
		Description: "The purpose of this tool is to determine if it's worth going ahead with creating a donation message based on the content " +
			"of the messages",
	}, func(ctx agent.Context, args TTSEvaluatorArgs) (TTSEvaluatorResult, error) {
		return isWorthy(args.Messages), nil
	})
}

func isWorthy(messages []string) TTSEvaluatorResult {
	res := TTSEvaluatorResult{}
	if len(messages) > 10 {
		res.Worthy = true
	}
	slog.Info("isWorthy()", "res.Worthy", res.Worthy)
	return res
}
