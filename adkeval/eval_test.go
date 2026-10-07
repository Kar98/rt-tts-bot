package adkeval

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/genai"
)

func evalSetOf(n int) *EvalSet {
	s := &EvalSet{EvalSetID: "par"}
	for i := range n {
		s.EvalCases = append(s.EvalCases, EvalCase{
			EvalID:       fmt.Sprintf("case%d", i),
			SessionInput: &SessionInput{AppName: "test_app", UserID: "u", State: map[string]any{"tone": "song"}},
			Conversation: []Invocation{{
				UserContent: NewContent(genai.NewContentFromText(fmt.Sprintf("message %d", i), genai.RoleUser)),
			}},
		})
	}
	return s
}

func TestRunParallel(t *testing.T) {
	agentLLM := &fakeLLM{delay: 30 * time.Millisecond, reply: func(prompt string) (string, error) {
		if prompt == "message 5" {
			return "", errors.New("model unavailable")
		}
		return "reply to " + prompt, nil
	}}
	a, err := llmagent.New(llmagent.Config{Name: "test_agent", Model: agentLLM, Instruction: "Tone is {tone}."})
	require.NoError(t, err)

	// Cases call the judge concurrently.
	var mu sync.Mutex
	var judged []string
	judge := fakeJudgeFactory(func(prompt string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		judged = append(judged, prompt)
		return "ID: polite\nProperty: The reply is polite.\nRationale: fine\nVerdict: yes\n", nil
	})
	cfg, err := parseConfig([]byte(`{"criteria":{
		"tool_trajectory_avg_score": 1.0,
		"rubric_based_final_response_quality_v1": {"threshold": 0.5, "judge_model_options": {"num_samples": 1},
			"rubrics": [{"rubric_id": "polite", "rubric_content": {"text_property": "The reply is polite."}}]}
	}}`))
	require.NoError(t, err)

	var samples []JudgeSample
	set := evalSetOf(8)
	res, err := Run(t.Context(), Config{
		OnJudgeSample: func(s JudgeSample) {
			mu.Lock()
			defer mu.Unlock()
			samples = append(samples, s)
		},
		EvalSet:     set,
		EvalConfig:  cfg,
		Agent:       a,
		JudgeModel:  judge,
		Parallelism: 4,
		PrepareCase: func(c *EvalCase) error {
			c.SessionInput.State["prepared"] = true
			return nil
		},
	})
	require.NoError(t, err)

	assert.LessOrEqual(t, agentLLM.maxInFlight.Load(), int32(4))
	assert.Greater(t, agentLLM.maxInFlight.Load(), int32(1), "cases should overlap")
	_, touched := set.EvalCases[0].SessionInput.State["prepared"]
	assert.False(t, touched, "PrepareCase must get a copy")

	assert.True(t, strings.HasPrefix(res.EvalSetResultID, "test_agent_par_"))
	require.Len(t, res.EvalCaseResults, 8)
	for i, c := range res.EvalCaseResults {
		assert.Equal(t, fmt.Sprintf("case%d", i), c.EvalID, "results keep eval set order")
		if i == 5 {
			assert.Equal(t, StatusFailed, c.FinalEvalStatus)
			assert.ErrorContains(t, c.Err, "model unavailable")
			assert.Empty(t, c.OverallEvalMetricResults)
			continue
		}
		require.NoError(t, c.Err)
		assert.Equal(t, StatusPassed, c.FinalEvalStatus)
		assert.Equal(t, "test_app", c.SessionDetails.AppName)
		assert.Equal(t, true, c.SessionDetails.State["prepared"])
		require.Len(t, c.OverallEvalMetricResults, 2)
		assert.Equal(t, "tool_trajectory_avg_score", c.OverallEvalMetricResults[0].MetricName)

		require.Len(t, c.EvalMetricResultPerInvocation, 1)
		per := c.EvalMetricResultPerInvocation[0]
		require.Len(t, per.EvalMetricResults, 2)
		actual := per.ActualInvocation
		assert.Equal(t, fmt.Sprintf("reply to message %d", i), contentText(actual.FinalResponse.GenAI()))
		require.NotNil(t, actual.AppDetails)
		assert.Contains(t, actual.AppDetails.AgentDetails["test_agent"].Instructions, "Tone is song.",
			"app_details holds the instruction after state is filled in")
		require.Len(t, actual.IntermediateData.InvocationEvents, 1)
		assert.Nil(t, actual.IntermediateData.InvocationEvents[0].Content, "the final event's text isn't repeated")
		assert.Equal(t, "fake-1", actual.IntermediateData.InvocationEvents[0].ModelVersion)
		assert.NotNil(t, actual.Duration)
	}
	assert.Len(t, judged, 7)

	require.Len(t, samples, 7, "one judge call per case that reached scoring")
	var ids []string
	for _, s := range samples {
		ids = append(ids, s.EvalID)
		assert.Equal(t, "rubric_based_final_response_quality_v1", s.Metric)
		assert.Equal(t, 1, s.Turn)
		assert.Equal(t, 1, s.Sample)
		assert.Contains(t, s.Prompt, "Tone is song.")
		assert.Contains(t, s.Reply, "Verdict: yes")
		assert.NoError(t, s.Err)
	}
	assert.NotContains(t, ids, "case5", "case5 failed before scoring")
	assert.Contains(t, ids, "case7")

	path, err := WriteResult(t.TempDir(), res)
	require.NoError(t, err)
	_, err = LoadResult(path)
	require.NoError(t, err)
}

func TestRunCaseStatus(t *testing.T) {
	assert.Equal(t, StatusFailed, caseStatus([]EvalStatus{StatusPassed, StatusFailed, StatusNotEvaluated}))
	assert.Equal(t, StatusPassed, caseStatus([]EvalStatus{StatusNotEvaluated, StatusPassed}))
	assert.Equal(t, StatusNotEvaluated, caseStatus([]EvalStatus{StatusNotEvaluated}))
	assert.Equal(t, StatusNotEvaluated, caseStatus(nil))
}

func TestRunMetricErrorIsNotEvaluated(t *testing.T) {
	agentLLM := &fakeLLM{reply: func(string) (string, error) { return "hello", nil }}
	a, err := llmagent.New(llmagent.Config{Name: "a", Model: agentLLM})
	require.NoError(t, err)
	// No rubrics anywhere: the rubric metric errors.
	cfg, err := parseConfig([]byte(`{"criteria":{"rubric_based_final_response_quality_v1":0.5}}`))
	require.NoError(t, err)
	res, err := Run(t.Context(), Config{EvalSet: evalSetOf(1), EvalConfig: cfg, Agent: a, JudgeModel: fakeJudgeFactory(nil)})
	require.NoError(t, err)
	c := res.EvalCaseResults[0]
	assert.Equal(t, StatusNotEvaluated, c.FinalEvalStatus)
	assert.Equal(t, StatusNotEvaluated, c.OverallEvalMetricResults[0].EvalStatus)
	assert.ErrorContains(t, c.OverallEvalMetricResults[0].Err, "rubrics are required")
}

func TestRunScenarioCaseFails(t *testing.T) {
	agentLLM := &fakeLLM{reply: func(string) (string, error) { return "hello", nil }}
	a, err := llmagent.New(llmagent.Config{Name: "a", Model: agentLLM})
	require.NoError(t, err)
	set, err := LoadEvalSet("testdata/legacy.evalset.json")
	require.NoError(t, err)
	cfg, err := parseConfig([]byte(`{"criteria":{"tool_trajectory_avg_score":1.0}}`))
	require.NoError(t, err)
	res, err := Run(t.Context(), Config{EvalSet: set, EvalConfig: cfg, Agent: a, Parallelism: 1})
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, res.EvalCaseResults[0].FinalEvalStatus, "agent made no get_weather call")
	assert.ErrorContains(t, res.EvalCaseResults[1].Err, "conversation_scenario is not supported")
}
