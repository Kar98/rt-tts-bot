package donogenerator

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"

	"github.com/Kar98/artosis-tts-agent/adkeval"
	"github.com/Kar98/artosis-tts-agent/adkeval/adkevaltest"
	"github.com/Kar98/artosis-tts-agent/internal/tools"
)

// TestDonoEval runs the eval set, scores it with the metrics in
// evals/test_config.json and writes the results to .adk/eval_history (upload
// them with `just eval-upload`). It calls Vertex AI, so it only runs with
// RUN_EVALS=1 (see `just eval-dono`). EVAL_PARALLELISM sets how many cases run
// at once.
func TestDonoEval(t *testing.T) {
	if os.Getenv("RUN_EVALS") == "" {
		t.Skip("set RUN_EVALS=1 to run")
	}
	ctx := t.Context()

	set, err := adkeval.LoadEvalSet("evals/dono_generator.evalset.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := adkeval.LoadConfig("evals/test_config.json")
	if err != nil {
		t.Fatal(err)
	}
	parallelism, err := strconv.Atoi(envOr("EVAL_PARALLELISM", "4"))
	if err != nil {
		t.Fatalf("EVAL_PARALLELISM: %v", err)
	}

	m, err := newModel(ctx, envOr("MODEL", "gemini-3.5-flash"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewDonoGenerator(m)
	if err != nil {
		t.Fatal(err)
	}

	adkevaltest.Run(t, adkeval.Config{
		EvalSet:     set,
		EvalConfig:  cfg,
		Agent:       a,
		JudgeModel:  newModel,
		Parallelism: parallelism,
		// The eval set only has the tone. Add the examples set_tone would put
		// in state for it.
		PrepareCase: func(c *adkeval.EvalCase) error {
			if c.SessionInput == nil {
				return fmt.Errorf("case has no session_input")
			}
			tone, _ := c.SessionInput.State[tools.TTSSetToneKey].(string)
			examples, err := tools.DonoExamples(tools.Tone(tone))
			if err != nil {
				return err
			}
			c.SessionInput.State[tools.TTSSetDonoExamples] = examples
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
