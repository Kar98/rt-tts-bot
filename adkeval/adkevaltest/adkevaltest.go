// Package adkevaltest runs an adkeval eval set from a Go test.
package adkevaltest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Kar98/artosis-tts-agent/adkeval"
)

// Run evaluates cfg, writes the result file to outDir and logs a summary.
// Each case is a subtest, which fails if the case's status is FAILED. The
// result file is written before any case fails.
func Run(t *testing.T, cfg adkeval.Config, outDir string) *adkeval.EvalSetResult {
	t.Helper()
	res, err := adkeval.Run(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	path, err := adkeval.WriteResult(outDir, res)
	if err != nil {
		t.Fatalf("writing results: %v", err)
	}
	t.Logf("results: %s", path)

	for _, c := range res.EvalCaseResults {
		t.Run(c.EvalID, func(t *testing.T) {
			t.Log(summary(c))
			if c.FinalEvalStatus == adkeval.StatusFailed {
				t.Errorf("%s: %s", c.EvalID, c.FinalEvalStatus)
			}
		})
	}
	return res
}

// summary describes a case result: its status, each metric's score against
// its threshold, each rubric verdict, and the agent's final responses.
func summary(c adkeval.EvalCaseResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", c.EvalID, c.FinalEvalStatus)
	if c.Err != nil {
		fmt.Fprintf(&b, "  error: %v\n", c.Err)
	}
	for _, m := range c.OverallEvalMetricResults {
		fmt.Fprintf(&b, "  %s: %s, score %s, threshold %s\n", m.MetricName, m.EvalStatus, num(m.Score), num(m.Threshold))
		if m.Err != nil {
			fmt.Fprintf(&b, "    error: %v\n", m.Err)
		}
	}
	for i, inv := range c.EvalMetricResultPerInvocation {
		fmt.Fprintf(&b, "  turn %d response: %s\n", i+1, responseText(inv.ActualInvocation))
		for _, m := range inv.EvalMetricResults {
			for _, r := range m.Details.RubricScores {
				rationale := ""
				if r.Rationale != nil {
					rationale = *r.Rationale
				}
				fmt.Fprintf(&b, "    %s/%s: %s %s\n", m.MetricName, r.RubricID, verdict(r.Score), rationale)
			}
		}
	}
	return b.String()
}

func responseText(inv adkeval.Invocation) string {
	if inv.FinalResponse == nil {
		return "(none)"
	}
	var texts []string
	for _, p := range inv.FinalResponse.GenAI().Parts {
		if p.Text != "" && !p.Thought {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func num(f *float64) string {
	if f == nil {
		return "none"
	}
	return fmt.Sprintf("%.2f", *f)
}

func verdict(score *float64) string {
	switch {
	case score == nil:
		return "unparsed"
	case *score == 1:
		return "yes"
	default:
		return "no"
	}
}
