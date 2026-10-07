// Package adkevaltest runs an adkeval eval set from a Go test.
package adkevaltest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Kar98/artosis-tts-agent/adkeval"
)

// Run evaluates cfg, writes the result file to outDir and logs a summary.
// Each case is a subtest, which fails if the case's status is FAILED. The
// result file is written before any case fails.
//
// Every LLM judge reply is also written, in full, next to the result file:
// <result name>.judge/<eval_id>/<metric>/turn<N>_sample<M>.txt. The result
// file keeps only one rationale per rubric.
func Run(t *testing.T, cfg adkeval.Config, outDir string) *adkeval.EvalSetResult {
	t.Helper()
	var mu sync.Mutex
	var samples []adkeval.JudgeSample
	next := cfg.OnJudgeSample
	cfg.OnJudgeSample = func(s adkeval.JudgeSample) {
		mu.Lock()
		samples = append(samples, s)
		mu.Unlock()
		if next != nil {
			next(s)
		}
	}

	res, err := adkeval.Run(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	path, err := adkeval.WriteResult(outDir, res)
	if err != nil {
		t.Fatalf("writing results: %v", err)
	}
	t.Logf("results: %s", path)
	if len(samples) > 0 {
		dir := filepath.Join(outDir, res.EvalSetResultName+".judge")
		if err := WriteJudgeSamples(dir, samples); err != nil {
			t.Fatalf("writing judge replies: %v", err)
		}
		t.Logf("judge replies: %s", dir)
	}

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

// WriteJudgeSamples writes each sample to
// dir/<eval_id>/<metric>/turn<N>_sample<M>.txt: a short header, the prompt
// the judge got, then its reply.
func WriteJudgeSamples(dir string, samples []adkeval.JudgeSample) error {
	for _, s := range samples {
		caseDir := filepath.Join(dir, s.EvalID, s.Metric)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "eval_id: %s\nmetric: %s\nturn: %d\nsample: %d\n", s.EvalID, s.Metric, s.Turn, s.Sample)
		if s.Err != nil {
			fmt.Fprintf(&b, "error: %v\n", s.Err)
		}
		fmt.Fprintf(&b, "\n===== PROMPT =====\n%s\n\n===== REPLY =====\n%s\n", s.Prompt, s.Reply)
		name := fmt.Sprintf("turn%d_sample%d.txt", s.Turn, s.Sample)
		if err := os.WriteFile(filepath.Join(caseDir, name), []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}
