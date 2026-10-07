package adkeval

import (
	"context"
	"sync"

	"google.golang.org/adk/v2/model"
)

// Evaluator scores one eval case on one metric. actual and expected have one
// entry per turn. Evaluate is called for several cases at once, so it must be
// safe for concurrent use.
type Evaluator interface {
	Evaluate(ctx context.Context, actual, expected []Invocation) (MetricResult, error)
}

// MetricResult is an Evaluator's result for a case.
type MetricResult struct {
	Score        *float64
	Status       EvalStatus
	RubricScores []RubricScore
	// PerInvocation has one entry per turn, in order.
	PerInvocation []InvocationResult
}

// InvocationResult is an Evaluator's result for one turn.
type InvocationResult struct {
	Score        *float64
	Status       EvalStatus
	RubricScores []RubricScore
}

// metricFactory builds an Evaluator from a criterion. judges gives it a judge
// model by name.
type metricFactory func(ctx context.Context, c Criterion, judges *judgeModels) (Evaluator, error)

var metrics = map[string]metricFactory{
	"tool_trajectory_avg_score":                     newTrajectoryEvaluator,
	"rubric_based_final_response_quality_v1":        newFinalResponseQualityEvaluator,
	"rubric_based_tool_use_quality_v1":              newToolUseQualityEvaluator,
	"rubric_based_multi_turn_trajectory_quality_v1": newMultiTurnTrajectoryEvaluator,
}

// judgeModels creates each judge model once per run and shares it between
// cases.
type judgeModels struct {
	newModel func(ctx context.Context, name string) (model.LLM, error)

	mu     sync.Mutex
	models map[string]model.LLM
}

func (j *judgeModels) get(ctx context.Context, name string) (model.LLM, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if m, ok := j.models[name]; ok {
		return m, nil
	}
	m, err := j.newModel(ctx, name)
	if err != nil {
		return nil, err
	}
	if j.models == nil {
		j.models = map[string]model.LLM{}
	}
	j.models[name] = m
	return m, nil
}

func statusFor(score *float64, threshold float64) EvalStatus {
	if score == nil {
		return StatusNotEvaluated
	}
	if *score >= threshold {
		return StatusPassed
	}
	return StatusFailed
}

// mean returns the mean of the non-nil values, or nil if there are none.
func mean(vals []*float64) *float64 {
	var sum float64
	var n int
	for _, v := range vals {
		if v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return ptr(sum / float64(n))
}
