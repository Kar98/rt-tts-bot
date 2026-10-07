package adkeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"
)

// DefaultParallelism is how many cases Run evaluates at once when
// Config.Parallelism is 0. adk-python uses the same default.
const DefaultParallelism = 4

// Config says what Run evaluates and how.
type Config struct {
	EvalSet    *EvalSet
	EvalConfig *EvalConfig
	Agent      agent.Agent

	// PrepareCase, if set, can change each case before it runs, for example
	// to add session state that isn't in the eval set file. It gets a copy, so
	// it can edit freely.
	PrepareCase func(*EvalCase) error

	// JudgeModel returns the model for a judge_model name. The default creates
	// a Gemini model on Vertex AI, configured by GOOGLE_CLOUD_PROJECT and
	// GOOGLE_CLOUD_LOCATION.
	JudgeModel func(ctx context.Context, name string) (model.LLM, error)

	// Parallelism caps how many cases run at once. 0 means
	// DefaultParallelism; 1 runs them one at a time.
	Parallelism int
}

// Run evaluates every case in the eval set and returns the results in eval
// set order. A case that fails doesn't stop the others. Run returns an error
// only for a bad Config or a cancelled ctx.
func Run(ctx context.Context, cfg Config) (*EvalSetResult, error) {
	if cfg.EvalSet == nil || cfg.EvalConfig == nil || cfg.Agent == nil {
		return nil, errors.New("adkeval: Config needs EvalSet, EvalConfig and Agent")
	}
	parallelism := cfg.Parallelism
	if parallelism == 0 {
		parallelism = DefaultParallelism
	}
	if parallelism < 0 {
		return nil, fmt.Errorf("adkeval: Parallelism is %d", parallelism)
	}
	newJudge := cfg.JudgeModel
	if newJudge == nil {
		newJudge = vertexJudge
	}

	judges := &judgeModels{newModel: newJudge}
	evaluators := make([]Evaluator, len(cfg.EvalConfig.Criteria))
	for i, c := range cfg.EvalConfig.Criteria {
		ev, err := metrics[c.MetricName](ctx, c, judges)
		if err != nil {
			return nil, err
		}
		evaluators[i] = ev
	}

	start := time.Now()
	id, name := newResultID(cfg.Agent.Name(), cfg.EvalSet.EvalSetID, start)
	result := &EvalSetResult{
		EvalSetResultID:   id,
		EvalSetResultName: name,
		EvalSetID:         cfg.EvalSet.EvalSetID,
		EvalCaseResults:   make([]EvalCaseResult, len(cfg.EvalSet.EvalCases)),
		CreationTimestamp: float64(start.UnixMicro()) / 1e6,
	}

	// No errgroup context: one case failing must not cancel the rest.
	var g errgroup.Group
	g.SetLimit(parallelism)
	for i, c := range cfg.EvalSet.EvalCases {
		g.Go(func() error {
			result.EvalCaseResults[i] = runCase(ctx, cfg, evaluators, c)
			return nil
		})
	}
	g.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func runCase(ctx context.Context, cfg Config, evaluators []Evaluator, c EvalCase) EvalCaseResult {
	res := EvalCaseResult{
		EvalSetFile:                   cfg.EvalSet.EvalSetID,
		EvalSetID:                     cfg.EvalSet.EvalSetID,
		EvalID:                        c.EvalID,
		FinalEvalStatus:               StatusFailed,
		OverallEvalMetricResults:      []EvalMetricResult{},
		EvalMetricResultPerInvocation: []EvalMetricResultPerInvocation{},
	}
	fail := func(err error) EvalCaseResult {
		res.Err = err
		return res
	}

	c, err := copyCase(c)
	if err != nil {
		return fail(err)
	}
	if cfg.PrepareCase != nil {
		if err := cfg.PrepareCase(&c); err != nil {
			return fail(fmt.Errorf("PrepareCase: %w", err))
		}
	}

	actual, session, err := runInference(ctx, cfg.Agent, c)
	if err != nil {
		return fail(fmt.Errorf("running agent: %w", err))
	}
	res.SessionDetails = session
	res.SessionID = session.ID
	res.UserID = session.UserID

	expected := c.Conversation
	if err := copyRubrics(c, actual); err != nil {
		return fail(err)
	}

	for i := range actual {
		res.EvalMetricResultPerInvocation = append(res.EvalMetricResultPerInvocation, EvalMetricResultPerInvocation{
			ActualInvocation:   actual[i],
			ExpectedInvocation: &expected[i],
			EvalMetricResults:  []EvalMetricResult{},
		})
	}

	var statuses []EvalStatus
	for i, ev := range evaluators {
		crit := cfg.EvalConfig.Criteria[i]
		mr, err := ev.Evaluate(ctx, actual, expected)
		if err != nil {
			mr = MetricResult{Status: StatusNotEvaluated}
		}
		overall := metricResult(crit, mr.Score, mr.Status, mr.RubricScores)
		overall.Err = err
		res.OverallEvalMetricResults = append(res.OverallEvalMetricResults, overall)
		statuses = append(statuses, mr.Status)
		for j, inv := range mr.PerInvocation {
			if j >= len(res.EvalMetricResultPerInvocation) {
				break
			}
			per := &res.EvalMetricResultPerInvocation[j]
			per.EvalMetricResults = append(per.EvalMetricResults, metricResult(crit, inv.Score, inv.Status, inv.RubricScores))
		}
	}
	res.FinalEvalStatus = caseStatus(statuses)
	return res
}

func metricResult(c Criterion, score *float64, status EvalStatus, rubrics []RubricScore) EvalMetricResult {
	return EvalMetricResult{
		MetricName: c.MetricName,
		Threshold:  ptr(c.Threshold),
		Criterion:  c.Raw,
		Score:      score,
		EvalStatus: status,
		Details:    EvalMetricResultDetails{RubricScores: rubrics},
	}
}

// caseStatus is FAILED if any metric failed, else PASSED if any passed, else
// NOT_EVALUATED.
func caseStatus(statuses []EvalStatus) EvalStatus {
	out := StatusNotEvaluated
	for _, s := range statuses {
		switch s {
		case StatusFailed:
			return StatusFailed
		case StatusPassed:
			out = StatusPassed
		}
	}
	return out
}

// copyRubrics adds the case's rubrics, and each expected turn's rubrics, to
// the matching actual turns, as adk-python does before scoring. A repeated
// rubric_id is an error.
func copyRubrics(c EvalCase, actual []Invocation) error {
	for i := range actual {
		var extra []Rubric
		extra = append(extra, c.Rubrics...)
		if i < len(c.Conversation) {
			extra = append(extra, c.Conversation[i].Rubrics...)
		}
		seen := map[string]bool{}
		for _, r := range actual[i].Rubrics {
			seen[r.RubricID] = true
		}
		for _, r := range extra {
			if seen[r.RubricID] {
				return fmt.Errorf("rubric_id %q already exists", r.RubricID)
			}
			seen[r.RubricID] = true
			actual[i].Rubrics = append(actual[i].Rubrics, r)
		}
	}
	return nil
}

// copyCase deep-copies c through JSON, so PrepareCase can't change the
// caller's eval set or race with another case.
func copyCase(c EvalCase) (EvalCase, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return EvalCase{}, err
	}
	var out EvalCase
	err = json.Unmarshal(b, &out)
	return out, err
}

func vertexJudge(ctx context.Context, name string) (model.LLM, error) {
	return gemini.NewModel(ctx, name, &genai.ClientConfig{Backend: genai.BackendVertexAI})
}
