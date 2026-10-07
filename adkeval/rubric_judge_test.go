package adkeval

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func rubric(id, text, typ string) Rubric {
	return Rubric{RubricID: id, RubricContent: RubricContent{TextProperty: &text}, Type: typ}
}

const twoVerdicts = `STEP 1: thinking
ID: funny
Property: The message is funny.
Rationale: It made me laugh.
Verdict: yes

ID: short
Property: The message is short.
Rationale: It rambles.
Verdict: no
`

func TestParseVerdicts(t *testing.T) {
	got := parseVerdicts(twoVerdicts)
	require.Len(t, got, 2)
	assert.Equal(t, "funny", got[0].rubricID)
	assert.Equal(t, "The message is funny.", got[0].property)
	assert.Equal(t, "It made me laugh.", got[0].rationale)
	assert.Equal(t, 1.0, *got[0].score)
	assert.Equal(t, "short", got[1].rubricID)
	assert.Equal(t, 0.0, *got[1].score)

	// A missing ID line must not borrow the next property's id.
	got = parseVerdicts("Property: A\nRationale: r\nVerdict: yes\nID: b\nProperty: B\nRationale: r\nVerdict: no\n")
	require.Len(t, got, 2)
	assert.Equal(t, "", got[0].rubricID)
	assert.Equal(t, "b", got[1].rubricID)

	// Counts that don't line up mean nothing is trusted.
	assert.Empty(t, parseVerdicts("Property: A\nRationale: r\nVerdict: yes\nProperty: B\nRationale: r\n"))

	got = parseVerdicts("Property: A\nRationale: r\nVerdict: maybe\n")
	require.Len(t, got, 1)
	assert.Nil(t, got[0].score)
}

func TestMatchRubrics(t *testing.T) {
	rubrics := []Rubric{rubric("funny", "The message is funny.", ""), rubric("tone", "It uses the “song” tone.", "")}
	got := matchRubrics([]verdict{
		{rubricID: "funny", score: ptr(1.0), rationale: "a"},
		// No id: matched by text despite markdown and plain quotes.
		{property: `**It uses the "song" tone.**`, score: ptr(0.0), rationale: "b"},
		{rubricID: "unknown", property: "Something else.", score: ptr(1.0)},
	}, rubrics)
	require.Len(t, got, 2)
	assert.Equal(t, "funny", got[0].RubricID)
	assert.Equal(t, "tone", got[1].RubricID)
	assert.Equal(t, "b", *got[1].Rationale)
}

func TestMajorityVote(t *testing.T) {
	yes := func(id string) RubricScore { return RubricScore{RubricID: id, Score: ptr(1.0), Rationale: ptr("y")} }
	no := func(id string) RubricScore { return RubricScore{RubricID: id, Score: ptr(0.0), Rationale: ptr("n")} }
	none := RubricScore{RubricID: "c"}
	got := majorityVote([][]RubricScore{
		{yes("a"), yes("b"), none},
		{yes("a"), no("b"), none},
		{no("a")},
	})
	require.Len(t, got, 3)
	assert.Equal(t, 1.0, *got[0].Score, "2 yes beat 1 no")
	assert.Equal(t, 0.0, *got[1].Score, "a tie is no")
	assert.Equal(t, "n", *got[1].Rationale, "rationale comes from the winning side")
	assert.Nil(t, got[2].Score, "no verdict in any sample stays null")
}

func TestEffectiveRubrics(t *testing.T) {
	j := &rubricJudge{rubricType: "FINAL_RESPONSE_QUALITY", rubrics: []Rubric{rubric("c1", "x", "")}}
	got, err := j.effectiveRubrics(Invocation{Rubrics: []Rubric{
		rubric("i1", "x", "FINAL_RESPONSE_QUALITY"),
		rubric("i2", "x", "TOOL_USE_QUALITY"),
		rubric("i3", "x", ""),
	}})
	require.NoError(t, err)
	var ids []string
	for _, r := range got {
		ids = append(ids, r.RubricID)
	}
	assert.Equal(t, []string{"c1", "i1"}, ids, "invocation rubrics need the metric's type")

	_, err = j.effectiveRubrics(Invocation{Rubrics: []Rubric{rubric("c1", "x", "FINAL_RESPONSE_QUALITY")}})
	assert.ErrorContains(t, err, "conflicts")

	_, err = (&rubricJudge{rubricType: "X"}).effectiveRubrics(Invocation{})
	assert.ErrorContains(t, err, "rubrics are required")
}

func TestPromptsHaveNoLeftoverPlaceholders(t *testing.T) {
	placeholder := regexp.MustCompile(`\{[a-z_]+\}`)
	for name, p := range map[string]string{
		"final": finalResponsePrompt, "tool use": toolUsePrompt, "multi turn": multiTurnPrompt,
	} {
		assert.NotContains(t, p, "Placeholders:", name)
		assert.NotContains(t, p, "{{", name)
		filled := placeholder.ReplaceAllStringFunc(p, func(s string) string { return "" })
		assert.NotRegexp(t, placeholder, filled, name)
	}
	got := fillPrompt(toolUsePrompt, map[string]string{
		"tool_declarations": "TOOLS", "user_input": "INPUT", "tool_usage": "USAGE", "rubrics": "RUBRICS",
	})
	assert.NotRegexp(t, placeholder, got)
	for _, s := range []string{"TOOLS", "INPUT", "USAGE", "RUBRICS"} {
		assert.Contains(t, got, s)
	}
}

func TestJudgeSamplesAndSummary(t *testing.T) {
	// Samples run one at a time (ParallelismLimit 1), so okCalls needs no lock.
	var okCalls int
	llm := &fakeLLM{reply: func(prompt string) (string, error) {
		if strings.Contains(prompt, "FAIL") {
			return "", errors.New("judge down")
		}
		okCalls++
		switch {
		case okCalls == 1:
			return "ID: funny\nProperty: The message is funny.\nRationale: meh\nVerdict: no\n", nil
		default:
			return "ID: funny\nProperty: The message is funny.\nRationale: good\nVerdict: yes\n", nil
		}
	}}
	rubrics := []Rubric{rubric("funny", "The message is funny.", "")}
	j := &rubricJudge{threshold: 0.5, opts: judgeOptions{NumSamples: 3, ParallelismLimit: 1}, llm: llm}

	var got []JudgeSample
	ctx := withSampleHook(t.Context(), "c", func(s JudgeSample) { got = append(got, s) })
	res := j.judge(ctx, []judgeTask{{turn: 1, prompt: "ok", rubrics: rubrics}, {turn: 2, prompt: "FAIL", rubrics: rubrics}})
	require.Len(t, got, 6, "every sample is reported, failed ones too")
	var failed int
	for _, s := range got {
		if s.Err != nil {
			failed++
			assert.Equal(t, 2, s.Turn)
			assert.Empty(t, s.Reply)
		}
	}
	assert.Equal(t, 3, failed)
	require.Len(t, res, 2)
	assert.Equal(t, 1.0, *res[0].Score, "2 of 3 samples said yes")
	assert.Equal(t, StatusPassed, res[0].Status)
	assert.Equal(t, StatusNotEvaluated, res[1].Status, "a failed sample makes the turn NOT_EVALUATED")
	assert.Equal(t, int32(6), llm.calls.Load())

	sum := j.summarize(res)
	assert.Equal(t, 1.0, *sum.Score)
	assert.Equal(t, StatusPassed, sum.Status)
	require.Len(t, sum.RubricScores, 1)
	assert.Equal(t, aggregatedRationale, *sum.RubricScores[0].Rationale)
}

func TestFinalResponsePrompt(t *testing.T) {
	inv := Invocation{
		UserContent:   NewContent(genai.NewContentFromText("Make a dono", genai.RoleUser)),
		FinalResponse: NewContent(genai.NewContentFromText("Here is a dono", genai.RoleModel)),
		AppDetails: &AppDetails{AgentDetails: map[string]AgentDetails{
			"dono": {Name: "dono", Instructions: "Be funny. Tone: song", ToolDeclarations: []*Tool{}},
		}},
		IntermediateData: &IntermediateData{InvocationEvents: []InvocationEvent{{Author: "dono"}}},
	}
	var prompt string
	judges := &judgeModels{newModel: fakeJudgeFactory(func(p string) (string, error) {
		prompt = p
		return "ID: r\nProperty: p\nRationale: ok\nVerdict: yes\n", nil
	})}
	ev, err := newFinalResponseQualityEvaluator(t.Context(), Criterion{
		MetricName: "rubric_based_final_response_quality_v1",
		Threshold:  0.5,
		Raw:        []byte(`{"threshold":0.5,"judge_model_options":{"num_samples":1},"rubrics":[{"rubric_id":"r","rubric_content":{"text_property":"p"}}]}`),
	}, judges)
	require.NoError(t, err)
	res, err := ev.Evaluate(t.Context(), []Invocation{inv}, nil)
	require.NoError(t, err)
	assert.Equal(t, StatusPassed, res.Status)
	for _, want := range []string{
		"Be funny. Tone: song", "Make a dono", "Here is a dono", "*  [id: r] p",
		"No intermediate steps were taken.", `"tool_declarations": {`, `"dono": []`,
	} {
		assert.Contains(t, prompt, want)
	}
}

func TestToolSteps(t *testing.T) {
	inv := invWithCalls(&genai.FunctionCall{ID: "1", Name: "get_weather", Args: map[string]any{"city_name": "SF"}})
	inv.IntermediateData.InvocationEvents = append(inv.IntermediateData.InvocationEvents, InvocationEvent{
		Author: "agent",
		Content: NewContent(&genai.Content{Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
			ID: "1", Name: "get_weather", Response: map[string]any{"temp_c": 18},
		}}}}),
	})
	got := toolSteps(inv)
	assert.Contains(t, got, `"tool_calls_and_response"`)
	assert.Contains(t, got, `"city_name": "SF"`)
	assert.Contains(t, got, `"temp_c": 18`)

	unanswered := toolSteps(invWithCalls(call("f", nil)))
	assert.Contains(t, unanswered, `"tool_response": "None"`)
}

func TestDialogue(t *testing.T) {
	invs := []Invocation{
		{
			UserContent:   NewContent(genai.NewContentFromText("Weather in SF?", genai.RoleUser)),
			FinalResponse: NewContent(genai.NewContentFromText("18C.", genai.RoleModel)),
			IntermediateData: &IntermediateData{InvocationEvents: []InvocationEvent{
				{Author: "weather", Content: NewContent(&genai.Content{Parts: []*genai.Part{
					{Text: "Checking."},
					{FunctionCall: &genai.FunctionCall{Name: "get_weather", Args: map[string]any{"city": "SF"}}},
				}})},
				{Author: "weather", Content: NewContent(&genai.Content{Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{Name: "get_weather", Response: map[string]any{"temp_c": 18}}},
				}})},
				{Author: "weather"},
			}},
		},
		{UserContent: NewContent(genai.NewContentFromText("Thanks", genai.RoleUser))},
	}
	assert.Equal(t, strings.Join([]string{
		"USER TURN 1: Weather in SF?",
		"AGENT (weather) TURN 1: Checking.",
		`AGENT (weather) TURN 1 (tool call): get_weather({"city":"SF"})`,
		`AGENT (weather) TURN 1 (tool output): get_weather -> {"temp_c":18}`,
		"AGENT (weather) TURN 1: 18C.",
		"USER TURN 2: Thanks",
	}, "\n"), dialogue(invs))
}

func TestMultiTurnJudgesLastTurnOnly(t *testing.T) {
	var prompts []string
	judges := &judgeModels{newModel: fakeJudgeFactory(func(p string) (string, error) {
		prompts = append(prompts, p)
		return "ID: polite\nProperty: The agent is polite.\nRationale: ok\nVerdict: yes\n", nil
	})}
	ev, err := newMultiTurnTrajectoryEvaluator(t.Context(), Criterion{
		MetricName: "rubric_based_multi_turn_trajectory_quality_v1",
		Threshold:  0.5,
		Raw:        []byte(`{"threshold":0.5,"judge_model_options":{"num_samples":1},"rubrics":[{"rubric_id":"polite","rubric_content":{"text_property":"The agent is polite."}}]}`),
	}, judges)
	require.NoError(t, err)
	invs := []Invocation{
		{UserContent: NewContent(genai.NewContentFromText("one", genai.RoleUser))},
		{UserContent: NewContent(genai.NewContentFromText("two", genai.RoleUser))},
	}
	var got []JudgeSample
	ctx := withSampleHook(t.Context(), "case1", func(s JudgeSample) { got = append(got, s) })
	res, err := ev.Evaluate(ctx, invs, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, JudgeSample{
		EvalID: "case1", Metric: "rubric_based_multi_turn_trajectory_quality_v1", Turn: 2, Sample: 1,
		Prompt: prompts[0], Reply: got[0].Reply,
	}, got[0], "the multi-turn judge reports the last turn")
	require.Len(t, prompts, 1)
	assert.Contains(t, prompts[0], "USER TURN 1: one\nUSER TURN 2: two")
	assert.Contains(t, prompts[0], `"id": "polite"`)
	require.Len(t, res.PerInvocation, 2)
	assert.Equal(t, StatusNotEvaluated, res.PerInvocation[0].Status)
	assert.Equal(t, StatusPassed, res.PerInvocation[1].Status)
	assert.Equal(t, StatusPassed, res.Status)
}
