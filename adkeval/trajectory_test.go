package adkeval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func call(name string, args map[string]any) *genai.FunctionCall {
	return &genai.FunctionCall{Name: name, Args: args}
}

func TestTrajectoryMatches(t *testing.T) {
	a := call("a", map[string]any{"n": 1})
	aFloat := call("a", map[string]any{"n": 1.0})
	aOther := call("a", map[string]any{"n": 2})
	b := call("b", nil)
	c := call("c", map[string]any{})

	tests := []struct {
		name       string
		match      MatchType
		ignoreArgs bool
		actual     []*genai.FunctionCall
		expected   []*genai.FunctionCall
		want       bool
	}{
		{"exact same", MatchExact, false, []*genai.FunctionCall{a, b}, []*genai.FunctionCall{a, b}, true},
		{"exact 1 equals 1.0", MatchExact, false, []*genai.FunctionCall{a}, []*genai.FunctionCall{aFloat}, true},
		{"exact nil args equal empty args", MatchExact, false, []*genai.FunctionCall{call("c", nil)}, []*genai.FunctionCall{c}, true},
		{"exact wrong order", MatchExact, false, []*genai.FunctionCall{b, a}, []*genai.FunctionCall{a, b}, false},
		{"exact extra call", MatchExact, false, []*genai.FunctionCall{a, b, c}, []*genai.FunctionCall{a, b}, false},
		{"exact wrong args", MatchExact, false, []*genai.FunctionCall{aOther}, []*genai.FunctionCall{a}, false},
		{"exact ignore args", MatchExact, true, []*genai.FunctionCall{aOther}, []*genai.FunctionCall{a}, true},
		{"exact both empty", MatchExact, false, nil, nil, true},
		{"exact expected empty", MatchExact, false, []*genai.FunctionCall{a}, nil, false},
		{"in order with extras", MatchInOrder, false, []*genai.FunctionCall{c, a, c, b}, []*genai.FunctionCall{a, b}, true},
		{"in order wrong order", MatchInOrder, false, []*genai.FunctionCall{b, a}, []*genai.FunctionCall{a, b}, false},
		{"in order expected empty", MatchInOrder, false, []*genai.FunctionCall{a}, nil, true},
		{"in order missing", MatchInOrder, false, []*genai.FunctionCall{a}, []*genai.FunctionCall{a, b}, false},
		{"any order swapped", MatchAnyOrder, false, []*genai.FunctionCall{b, c, a}, []*genai.FunctionCall{a, b}, true},
		{"any order needs each once", MatchAnyOrder, false, []*genai.FunctionCall{a}, []*genai.FunctionCall{a, a}, false},
		{"any order expected empty", MatchAnyOrder, false, nil, nil, true},
		{"any order ignore args", MatchAnyOrder, true, []*genai.FunctionCall{b, aOther}, []*genai.FunctionCall{a}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &trajectoryEvaluator{matchType: tt.match, ignoreArgs: tt.ignoreArgs}
			assert.Equal(t, tt.want, e.matches(tt.actual, tt.expected))
		})
	}
}

func invWithCalls(calls ...*genai.FunctionCall) Invocation {
	var parts []*genai.Part
	for _, c := range calls {
		parts = append(parts, &genai.Part{FunctionCall: c})
	}
	return Invocation{IntermediateData: &IntermediateData{InvocationEvents: []InvocationEvent{
		{Author: "agent", Content: NewContent(&genai.Content{Role: "model", Parts: parts})},
	}}}
}

func TestTrajectoryEvaluate(t *testing.T) {
	e := &trajectoryEvaluator{threshold: 1, matchType: MatchExact}
	legacyExpected := Invocation{IntermediateData: &IntermediateData{Legacy: true, ToolUses: []*FunctionCall{{Name: "a"}}}}
	actual := []Invocation{invWithCalls(call("a", nil)), invWithCalls(call("b", nil))}
	expected := []Invocation{legacyExpected, invWithCalls(call("a", nil))}

	res, err := e.Evaluate(t.Context(), actual, expected)
	require.NoError(t, err)
	assert.Equal(t, 0.5, *res.Score)
	assert.Equal(t, StatusFailed, res.Status)
	require.Len(t, res.PerInvocation, 2)
	assert.Equal(t, StatusPassed, res.PerInvocation[0].Status)
	assert.Equal(t, StatusFailed, res.PerInvocation[1].Status)

	_, err = e.Evaluate(t.Context(), actual[:1], expected)
	assert.Error(t, err)

	res, err = e.Evaluate(t.Context(), actual, nil)
	require.NoError(t, err)
	assert.Equal(t, StatusNotEvaluated, res.Status)
}
