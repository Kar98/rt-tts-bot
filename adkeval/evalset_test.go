package adkeval

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMinimalEvalSet(t *testing.T) {
	s, err := LoadEvalSet("testdata/minimal.evalset.json")
	require.NoError(t, err)
	assert.Equal(t, "smoke", s.EvalSetID)
	require.Len(t, s.EvalCases, 1)
	inv := s.EvalCases[0].Conversation[0]
	assert.Equal(t, "hi", contentText(inv.UserContent.GenAI()))
	assert.Equal(t, "user", inv.UserContent.Role)
}

func TestLegacyEvalSetRoundTrip(t *testing.T) {
	s, err := LoadEvalSet("testdata/legacy.evalset.json")
	require.NoError(t, err)

	inv := s.EvalCases[0].Conversation[0]
	require.True(t, inv.IntermediateData.Legacy)
	calls := inv.toolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, map[string]any{"city_name": "SF", "days": float64(1)}, calls[0].Args)
	assert.Equal(t, "router", inv.IntermediateData.IntermediateResponses[0].Author)

	path := filepath.Join(t.TempDir(), "out.evalset.json")
	require.NoError(t, s.Save(path))
	again, err := LoadEvalSet(path)
	require.NoError(t, err)
	assert.Equal(t, mustJSON(t, s), mustJSON(t, again))

	out := mustJSON(t, s)
	assertSnakeKeys(t, out)
	// User data keeps its keys, snake_case or not.
	assert.Contains(t, out, `"city_name":"SF"`)
	assert.Contains(t, out, `"sky_state":"sunny"`)
	assert.Contains(t, out, `"preferred_units":"metric"`)
	// Unknown keys on EvalCase and SessionInput survive.
	assert.Contains(t, out, `"custom_case_key":{"keep":"me"}`)
	assert.Contains(t, out, `"custom_session_key":7`)
	assert.Contains(t, out, `"user_persona":"NOVICE"`)
	assert.Contains(t, out, `"intermediate_responses":[["router",[{"text":"Routing to weather"}]]]`)
}

func TestInvocationEventsRoundTrip(t *testing.T) {
	s, err := LoadEvalSet("testdata/events.evalset.json")
	require.NoError(t, err)
	inv := s.EvalCases[0].Conversation[0]
	require.False(t, inv.IntermediateData.Legacy)
	require.Len(t, inv.toolCalls(), 1)
	require.Len(t, inv.toolResponses(), 1)
	assert.Equal(t, int32(12), inv.IntermediateData.InvocationEvents[2].UsageMetadata.PromptTokenCount)

	out := mustJSON(t, s)
	assertSnakeKeys(t, out)
	assert.Contains(t, out, `"function_call":{"args":{"city_name":"SF"},"id":"fc1","name":"get_weather"}`)
	assert.Contains(t, out, `"prompt_token_count":12`)
	assert.Contains(t, out, `"content":null`)
}

func TestCamelCaseContentLoads(t *testing.T) {
	var inv Invocation
	err := json.Unmarshal([]byte(`{"user_content":{"role":"user","parts":[{"functionCall":{"name":"f","args":{"someArg":1}}}]}}`), &inv)
	require.NoError(t, err)
	fc := inv.UserContent.Parts[0].FunctionCall
	require.NotNil(t, fc)
	assert.Equal(t, map[string]any{"someArg": float64(1)}, fc.Args)
}

func TestSchemaPropertyNamesKept(t *testing.T) {
	in := `{"function_declarations":[{"name":"f","parameters":{"type":"OBJECT","properties":{"cityName":{"type":"STRING","max_length":5}}}}]}`
	var tool Tool
	require.NoError(t, json.Unmarshal([]byte(in), &tool))
	props := tool.FunctionDeclarations[0].Parameters.Properties
	require.Contains(t, props, "cityName")
	assert.NotNil(t, props["cityName"].MaxLength)

	out, err := json.Marshal(tool)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"properties":{"cityName":{"max_length":5,"type":"STRING"}}`)
	assert.Contains(t, string(out), `"function_declarations"`)
}

func TestEvalSetValidation(t *testing.T) {
	for name, in := range map[string]string{
		"no id":            `{"eval_cases":[]}`,
		"no eval_id":       `{"eval_set_id":"s","eval_cases":[{"conversation":[{"user_content":{"parts":[{"text":"x"}]}}]}]}`,
		"neither":          `{"eval_set_id":"s","eval_cases":[{"eval_id":"c"}]}`,
		"both":             `{"eval_set_id":"s","eval_cases":[{"eval_id":"c","conversation_scenario":{},"conversation":[{"user_content":{"parts":[{"text":"x"}]}}]}]}`,
		"no user_content":  `{"eval_set_id":"s","eval_cases":[{"eval_id":"c","conversation":[{}]}]}`,
		"bad legacy pairs": `{"eval_set_id":"s","eval_cases":[{"eval_id":"c","conversation":[{"user_content":{"parts":[{"text":"x"}]},"intermediate_data":{"intermediate_responses":[["a"]]}}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var s EvalSet
			err := json.Unmarshal([]byte(in), &s)
			if err == nil {
				err = s.validate()
			}
			assert.Error(t, err)
		})
	}
}

func TestResultIDAndFile(t *testing.T) {
	r := &EvalSetResult{EvalSetID: "set", EvalCaseResults: []EvalCaseResult{{
		EvalID:                        "c1",
		FinalEvalStatus:               StatusPassed,
		OverallEvalMetricResults:      []EvalMetricResult{{MetricName: "m", Score: ptr(1.0), EvalStatus: StatusPassed}},
		EvalMetricResultPerInvocation: []EvalMetricResultPerInvocation{},
	}}}
	r.EvalSetResultID, r.EvalSetResultName = newResultID("app/x", "set", mustTime(t, "2026-10-07T00:00:00.5Z"))
	assert.Equal(t, "app/x_set_1791331200.500000", r.EvalSetResultID)
	assert.Equal(t, "app_x_set_1791331200.500000", r.EvalSetResultName)

	path, err := WriteResult(t.TempDir(), r)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(path, "app_x_set_1791331200.500000.evalset_result.json"))
	got, err := LoadResult(path)
	require.NoError(t, err)
	assert.Equal(t, StatusPassed, got.EvalCaseResults[0].FinalEvalStatus)

	out := mustJSON(t, r)
	// adk-python writes these keys even when they are empty.
	for _, key := range []string{
		`"eval_metric_results":null`, `"session_details":null`, `"custom_function_path":null`,
		`"criterion":null`, `"rubric_scores":null`, `"token_usage_details":null`, `"final_eval_status":1`,
	} {
		assert.Contains(t, out, key)
	}
}

var camelKey = regexp.MustCompile(`"([a-z]+[A-Z][A-Za-z]*)":`)

// assertSnakeKeys fails if out has a camelCase key outside the fixtures' user
// data, which uses none.
func assertSnakeKeys(t *testing.T, out string) {
	t.Helper()
	assert.Empty(t, camelKey.FindAllString(out, -1))
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
