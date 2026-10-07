package summariser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"

	"github.com/Kar98/artosis-tts-agent/adkeval"
	"github.com/Kar98/artosis-tts-agent/adkeval/adkevaltest"
	"github.com/Kar98/artosis-tts-agent/internal/tools"
	"github.com/Kar98/artosis-tts-agent/internal/twitchchat"
)

// chatFileKey names, in each case's session_input.state, the chat log in
// internal/twitchchat/message_data to summarise.
const chatFileKey = "chat_file"

var evalDirs = []string{"evals", "evals/inline_example"}

// TestSummariserEval runs each eval set in evalDirs, scores it with the
// metrics in the test_config.json next to it and writes the results to
// .adk/eval_history (upload them with `just eval-upload file=<path>`). It
// calls Vertex AI, so it only runs with RUN_EVALS=1 (see
// `just eval-summariser`). EVAL_PARALLELISM sets how many cases run at once.
func TestSummariserEval(t *testing.T) {
	if os.Getenv("RUN_EVALS") == "" {
		t.Skip("set RUN_EVALS=1 to run")
	}
	ctx := t.Context()

	parallelism, err := strconv.Atoi(envOr("EVAL_PARALLELISM", "4"))
	if err != nil {
		t.Fatalf("EVAL_PARALLELISM: %v", err)
	}

	m, err := newModel(ctx, envOr("MODEL", "gemini-3.5-flash"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewSummariser(m)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range evalDirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			set, err := adkeval.LoadEvalSet(filepath.Join(dir, "summariser.evalset.json"))
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := adkeval.LoadConfig(filepath.Join(dir, "test_config.json"))
			if err != nil {
				t.Fatal(err)
			}
			runEval(t, a, set, cfg, parallelism)
		})
	}
}

func runEval(t *testing.T, a agent.Agent, set *adkeval.EvalSet, cfg *adkeval.EvalConfig, parallelism int) {
	adkevaltest.Run(t, adkeval.Config{
		EvalSet:     set,
		EvalConfig:  cfg,
		Agent:       a,
		JudgeModel:  newModel,
		Parallelism: parallelism,
		// Load the case's chat log into state, as read_twitch_chat would.
		PrepareCase: func(c *adkeval.EvalCase) error {
			if c.SessionInput == nil {
				return fmt.Errorf("case has no session_input")
			}
			file, _ := c.SessionInput.State[chatFileKey].(string)
			if file == "" {
				return fmt.Errorf("session_input.state.%s is not set", chatFileKey)
			}
			msgs, err := twitchchat.StoredSource{}.Fetch(t.Context(), twitchchat.Request{Channel: file})
			if err != nil {
				return err
			}
			c.SessionInput.State[tools.TranscriptStateKey] = twitchchat.FormatTranscript(msgs)
			return nil
		},
	}, ".adk/eval_history")
}

// newModel returns a Vertex AI Gemini model, for the agent and the judge.
func newModel(ctx context.Context, name string) (model.LLM, error) {
	return gemini.NewModel(ctx, name, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Location: envOr("MODEL_LOCATION", "global"),
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
