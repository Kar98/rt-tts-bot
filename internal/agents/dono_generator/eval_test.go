package donogenerator

import (
	"os"
	"testing"

	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"

	"github.com/Kar98/artosis-tts-agent/internal/evals"
	"github.com/Kar98/artosis-tts-agent/internal/tools"
)

// TestDonoEval runs the eval set as a Langfuse experiment. The Langfuse
// evaluators score it afterwards (see cmd/dono-eval-setup). It calls Vertex AI, so
// it only runs with RUN_EVALS=1 (see `just eval-dono`).
func TestDonoEval(t *testing.T) {
	if os.Getenv("RUN_EVALS") == "" {
		t.Skip("set RUN_EVALS=1 to run")
	}
	ctx := t.Context()

	set, err := evals.LoadEvalSet("evals/dono_generator.evalset.json")
	if err != nil {
		t.Fatal(err)
	}
	// Fill in the examples set_tone would add for each case's tone.
	for _, c := range set.Cases {
		tone, _ := c.State[tools.TTSSetToneKey].(string)
		examples, err := tools.DonoExamples(tools.Tone(tone))
		if err != nil {
			t.Fatalf("case %s: %v", c.ID, err)
		}
		c.State[tools.TTSSetDonoExamples] = examples
	}

	agentModel := envOr("MODEL", "gemini-3.5-flash")
	m, err := gemini.NewModel(ctx, agentModel, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Location: envOr("MODEL_LOCATION", "global"),
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewSummariser(m)
	if err != nil {
		t.Fatal(err)
	}

	suite := &evals.Suite{
		Set:      set,
		Agent:    a,
		Metadata: map[string]string{"model": agentModel},
	}
	suite.Run(t)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
