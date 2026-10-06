// Command dono-eval-setup creates the Langfuse evaluators that score the
// dono_generator experiments from `just eval-dono`, and the evaluation rule
// that runs them. It skips anything that already exists by name, so it is safe
// to rerun. Edit the evaluator prompts in Langfuse after creating them.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"slices"

	"github.com/Kar98/artosis-tts-agent/internal/observability"
	"github.com/Kar98/artosis-tts-agent/internal/prompt"
)

// Must match the eval_set_id in the eval set.
const datasetName = "dono_generator"

const ruleName = "dono_generator experiments"

var metrics = []string{"humour_quality", "factualness", "stream_tone"}

//go:embed judge.md
var judgeTemplate string

// variableMapping fills the {{variables}} in judge.md. "input" is the dataset
// item input that evals.Suite sets as the root span input.
var variableMapping = []map[string]any{
	{"variable": "message", "source": "input", "jsonPath": "$.input"},
	{"variable": "tone", "source": "input", "jsonPath": "$.session_state.dono_tone"},
	{"variable": "examples", "source": "input", "jsonPath": "$.session_state.dono_examples"},
	{"variable": "context", "source": "input", "jsonPath": "$.context"},
	{"variable": "output", "source": "output"},
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg, ok := observability.LoadLangfuseConfig(ctx, "")
	if !ok {
		return fmt.Errorf("set LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY")
	}
	lf := observability.NewLangfuseClient(cfg)

	datasetID, err := lf.UpsertDataset(ctx, datasetName, "")
	if err != nil {
		return err
	}

	existing, err := lf.ListEvaluators(ctx)
	if err != nil {
		return err
	}
	var assignments []map[string]any
	for _, m := range metrics {
		id := ""
		if i := slices.IndexFunc(existing, func(e observability.NamedID) bool { return e.Name == m }); i >= 0 {
			id = existing[i].ID
			log.Printf("evaluator %s exists (%s)", m, id)
		} else {
			id, err = lf.CreateEvaluator(ctx, map[string]any{
				"type":            "llm_as_judge",
				"name":            m,
				"prompt":          prompt.Render(judgeTemplate, map[string]string{"metric": m}),
				"variableMapping": variableMapping,
				"outputDefinition": map[string]any{
					"dataType":                   "NUMERIC",
					"minValue":                   1,
					"maxValue":                   5,
					"scoreValueInstructions":     "Integer from 1 (worst) to 5 (best).",
					"scoreReasoningInstructions": "One or two sentences explaining the score.",
				},
			})
			if err != nil {
				return fmt.Errorf("creating evaluator %s: %w", m, err)
			}
			log.Printf("created evaluator %s (%s)", m, id)
		}
		assignments = append(assignments, map[string]any{"evaluatorId": id})
	}

	rules, err := lf.ListEvaluationRules(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(rules, func(r observability.NamedID) bool { return r.Name == ruleName }) {
		log.Printf("rule %q exists; check its evaluators in Langfuse", ruleName)
		return nil
	}
	// Evaluators can't run without a model, and Langfuse rejects an enabled
	// rule that has none.
	conns, err := lf.CountLLMConnections(ctx)
	if err != nil {
		return err
	}
	enabled := conns > 0
	id, err := lf.CreateEvaluationRule(ctx, map[string]any{
		"name":    ruleName,
		"enabled": enabled,
		"filter": []map[string]any{
			{"type": "stringOptions", "column": "datasetId", "operator": "any of", "value": []string{datasetID}},
			{"type": "boolean", "column": "isExperimentItemRootSpan", "operator": "=", "value": true},
		},
		"evaluatorAssignments": assignments,
	})
	if err != nil {
		return fmt.Errorf("creating rule: %w", err)
	}
	log.Printf("created rule %q (%s), enabled=%v", ruleName, id, enabled)
	if !enabled {
		log.Printf("no LLM connection in Langfuse: add one under Settings > LLM Connections, set it as the default evaluation model, then enable the rule")
	}
	return nil
}
