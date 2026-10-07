package adkeval

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"google.golang.org/genai"
)

// MatchType says how tool_trajectory_avg_score compares tool calls. It is
// written as adk-python's integer value and also read from its name.
type MatchType int

const (
	// MatchExact needs the same calls in the same order.
	MatchExact MatchType = 0
	// MatchInOrder needs the expected calls in order, with extra calls
	// allowed between them.
	MatchInOrder MatchType = 1
	// MatchAnyOrder needs every expected call, in any order.
	MatchAnyOrder MatchType = 2
)

func (m *MatchType) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		if n < 0 || n > 2 {
			return fmt.Errorf("match_type %d is not 0, 1 or 2", n)
		}
		*m = MatchType(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("match_type must be a string or an integer: %w", err)
	}
	// adk-python accepts any case, with "-" or " " for "_".
	switch strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToUpper(s)) {
	case "EXACT":
		*m = MatchExact
	case "IN_ORDER":
		*m = MatchInOrder
	case "ANY_ORDER":
		*m = MatchAnyOrder
	default:
		return fmt.Errorf("unknown match_type %q", s)
	}
	return nil
}

type trajectoryEvaluator struct {
	threshold  float64
	matchType  MatchType
	ignoreArgs bool
}

func newTrajectoryEvaluator(_ context.Context, c Criterion, _ *judgeModels) (Evaluator, error) {
	var opts struct {
		MatchType  MatchType `json:"match_type"`
		IgnoreArgs bool      `json:"ignore_args"`
	}
	if err := json.Unmarshal(c.Raw, &opts); err != nil {
		return nil, fmt.Errorf("%s: %w", c.MetricName, err)
	}
	return &trajectoryEvaluator{threshold: c.Threshold, matchType: opts.MatchType, ignoreArgs: opts.IgnoreArgs}, nil
}

// Evaluate scores each turn 1 if its tool calls match the expected ones and 0
// if not. The case score is the mean.
func (e *trajectoryEvaluator) Evaluate(_ context.Context, actual, expected []Invocation) (MetricResult, error) {
	if len(expected) == 0 {
		return MetricResult{Status: StatusNotEvaluated}, nil
	}
	if len(actual) != len(expected) {
		return MetricResult{}, fmt.Errorf("agent took %d turns, eval set expects %d", len(actual), len(expected))
	}
	var res MetricResult
	var scores []*float64
	for i := range actual {
		score := 0.0
		if e.matches(actual[i].toolCalls(), expected[i].toolCalls()) {
			score = 1
		}
		scores = append(scores, &score)
		res.PerInvocation = append(res.PerInvocation, InvocationResult{
			Score:  &score,
			Status: statusFor(&score, e.threshold),
		})
	}
	res.Score = mean(scores)
	res.Status = statusFor(res.Score, e.threshold)
	return res, nil
}

func (e *trajectoryEvaluator) matches(actual, expected []*genai.FunctionCall) bool {
	switch e.matchType {
	case MatchInOrder:
		j := 0
		for _, a := range actual {
			if j < len(expected) && e.callsEqual(a, expected[j]) {
				j++
			}
		}
		return j == len(expected)
	case MatchAnyOrder:
		used := make([]bool, len(actual))
	next:
		for _, want := range expected {
			for i, a := range actual {
				if !used[i] && e.callsEqual(a, want) {
					used[i] = true
					continue next
				}
			}
			return false
		}
		return true
	default:
		if len(actual) != len(expected) {
			return false
		}
		for i := range actual {
			if !e.callsEqual(actual[i], expected[i]) {
				return false
			}
		}
		return true
	}
}

// callsEqual compares name and args, ignoring the call id. Args go through
// JSON so numbers compare by value: 1 equals 1.0.
func (e *trajectoryEvaluator) callsEqual(a, b *genai.FunctionCall) bool {
	if a.Name != b.Name {
		return false
	}
	if e.ignoreArgs {
		return true
	}
	return reflect.DeepEqual(normalizeJSON(a.Args), normalizeJSON(b.Args))
}

func normalizeJSON(v map[string]any) any {
	if len(v) == 0 {
		return map[string]any{}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}
