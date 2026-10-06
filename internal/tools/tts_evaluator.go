package tools

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

//go:embed existing_donos.json
var donoExamples string

// TTSEvaluatorArgs is empty. The model never sees the chat messages, since
// read_twitch_chat only returns a count, so the tool reads them from session
// state instead of asking the model for them.
type TTSEvaluatorArgs struct{}

type TTSEvaluatorResult struct {
	Worthy bool   `json:"worthy"`
	Error  string `json:"error,omitempty"`
}

type TTSSetToneArgs struct {
	Tone Tone `json:"tone" jsonschema:"the tone of the chat"`
}
type TTSSetResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Tone string

const (
	ToneHumour   Tone = "humour"
	ToneRandom   Tone = "random"
	ToneGross    Tone = "gross"
	TonePureSpam Tone = "pure_spam"
	ToneQuestion Tone = "question"
	ToneSong     Tone = "song"
)

const TTSEvaluatorToolname = "tts_evaluator"
const TTSSetToneToolName = "set_tone"
const TTSSetToneKey = "dono_tone"
const TTSSetDonoExamples = "dono_examples"

func NewTTSEvaluatorTool() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: TTSEvaluatorToolname,
		Description: "The purpose of this tool is to determine if it's worth going ahead with creating a donation message based on the content " +
			"of the messages saved by " + ReadChatToolName + ". Takes no arguments.",
	}, func(ctx agent.Context, _ TTSEvaluatorArgs) (TTSEvaluatorResult, error) {
		return evaluate(ctx.State()), nil
	})
}

func NewSetToneTool() (tool.Tool, error) {
	toneValues := []any{ToneHumour, ToneRandom, ToneGross, TonePureSpam, ToneQuestion, ToneSong}
	schema, err := jsonschema.For[TTSSetToneArgs](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[Tone](): {Type: "string", Enum: toneValues},
		},
	})
	if err != nil {
		return nil, err
	}
	donos, err := loadDonoExamples()
	if err != nil {
		return nil, err
	}

	return functiontool.New(functiontool.Config{
		Name:        TTSSetToneToolName,
		Description: "The purpose of this tool is to set the tone in the agent context so other agents can understand the underlying intention",
		InputSchema: schema,
	}, func(ctx agent.Context, args TTSSetToneArgs) (TTSSetResult, error) {
		if args.Tone == "" {
			return TTSSetResult{Error: "tone was blank"}, nil
		}
		ctx.State().Set(TTSSetToneKey, args.Tone)
		slog.Info("NewSetToneTool", "tone", args.Tone)
		// Set dono examples for next agent to use
		examples, err := formatExamples(donos, args.Tone)
		if err != nil {
			return TTSSetResult{Error: err.Error()}, err
		}
		if err := ctx.State().Set(TTSSetDonoExamples, examples); err != nil {
			return TTSSetResult{Error: err.Error()}, err
		}

		return TTSSetResult{Status: "set"}, nil
	})
}

// DonoExamples returns the example donations for tone in the form set_tone
// writes to state under TTSSetDonoExamples.
func DonoExamples(tone Tone) (string, error) {
	donos, err := loadDonoExamples()
	if err != nil {
		return "", err
	}
	return formatExamples(donos, tone)
}

func loadDonoExamples() (map[Tone][]string, error) {
	var donos map[Tone][]string
	if err := json.Unmarshal([]byte(donoExamples), &donos); err != nil {
		return nil, err
	}
	return donos, nil
}

func formatExamples(donos map[Tone][]string, tone Tone) (string, error) {
	examples, ok := donos[tone]
	if !ok {
		return "", fmt.Errorf("no examples for tone %s", tone)
	}
	var b strings.Builder
	for _, e := range examples {
		// one example per line, so the model can tell them apart
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(e, "\n", " "))
		b.WriteString("\n")
	}
	return b.String(), nil
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
