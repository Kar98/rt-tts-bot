package adkeval

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig("testdata/test_config.json")
	require.NoError(t, err)

	var names []string
	for _, c := range cfg.Criteria {
		names = append(names, c.MetricName)
	}
	// File order, not map order.
	assert.Equal(t, []string{
		"tool_trajectory_avg_score",
		"rubric_based_final_response_quality_v1",
		"rubric_based_tool_use_quality_v1",
		"rubric_based_multi_turn_trajectory_quality_v1",
	}, names)
	assert.Equal(t, 0.6, cfg.Criteria[1].Threshold)
	// A bare number is the threshold.
	assert.Equal(t, 0.5, cfg.Criteria[2].Threshold)
	assert.JSONEq(t, `{"threshold":0.5}`, string(cfg.Criteria[2].Raw))

	traj, err := newTrajectoryEvaluator(t.Context(), cfg.Criteria[0], nil)
	require.NoError(t, err)
	assert.Equal(t, MatchInOrder, traj.(*trajectoryEvaluator).matchType)
	assert.True(t, traj.(*trajectoryEvaluator).ignoreArgs)

	judges := &judgeModels{newModel: fakeJudgeFactory(nil)}
	ev, err := newFinalResponseQualityEvaluator(t.Context(), cfg.Criteria[1], judges)
	require.NoError(t, err)
	j := ev.(*finalResponseQualityEvaluator).j
	assert.Equal(t, judgeOptions{JudgeModel: "judge-a", NumSamples: 3, ParallelismLimit: 2}, j.opts)
	assert.Len(t, j.rubrics, 1)

	ev, err = newToolUseQualityEvaluator(t.Context(), cfg.Criteria[2], judges)
	require.NoError(t, err)
	assert.Equal(t, judgeOptions{JudgeModel: defaultJudgeModel, NumSamples: 5, ParallelismLimit: 1}, ev.(*toolUseQualityEvaluator).j.opts)
}

func TestConfigErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no criteria":       `{}`,
		"empty criteria":    `{"criteria":{}}`,
		"unsupported":       `{"criteria":{"response_match_score":0.8}}`,
		"missing threshold": `{"criteria":{"tool_trajectory_avg_score":{"match_type":"EXACT"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseConfig([]byte(in))
			assert.Error(t, err)
		})
	}

	_, err := parseConfig([]byte(`{"criteria":{"response_match_score":0.8}}`))
	assert.ErrorContains(t, err, "supported metrics: rubric_based_final_response_quality_v1")
}

func TestMatchTypeForms(t *testing.T) {
	for in, want := range map[string]MatchType{
		`"EXACT"`: MatchExact, `"in-order"`: MatchInOrder, `"Any Order"`: MatchAnyOrder, `1`: MatchInOrder, `2`: MatchAnyOrder,
	} {
		var m MatchType
		require.NoError(t, json.Unmarshal([]byte(in), &m), in)
		assert.Equal(t, want, m, in)
	}
	var m MatchType
	assert.Error(t, json.Unmarshal([]byte(`"SOMETIMES"`), &m))
	assert.Error(t, json.Unmarshal([]byte(`3`), &m))
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}
