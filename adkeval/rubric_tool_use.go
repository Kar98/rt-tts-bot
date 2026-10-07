package adkeval

import (
	"context"
	_ "embed"
)

//go:embed prompts/rubric_based_tool_use_quality_v1.txt
var toolUsePromptFile string

var toolUsePrompt = loadPrompt(toolUsePromptFile)

// toolUseQualityEvaluator is rubric_based_tool_use_quality_v1: it judges each
// turn's tool calls against TOOL_USE_QUALITY rubrics.
type toolUseQualityEvaluator struct{ j *rubricJudge }

func newToolUseQualityEvaluator(ctx context.Context, c Criterion, judges *judgeModels) (Evaluator, error) {
	j, err := newRubricJudge(ctx, c, judges, "TOOL_USE_QUALITY")
	if err != nil {
		return nil, err
	}
	return &toolUseQualityEvaluator{j}, nil
}

func (e *toolUseQualityEvaluator) Evaluate(ctx context.Context, actual, _ []Invocation) (MetricResult, error) {
	tasks := make([]judgeTask, len(actual))
	for i, inv := range actual {
		rubrics, err := e.j.effectiveRubrics(inv)
		if err != nil {
			return MetricResult{}, err
		}
		tasks[i] = judgeTask{turn: i + 1, rubrics: rubrics, prompt: fillPrompt(toolUsePrompt, map[string]string{
			"tool_declarations": toolDeclarations(inv.AppDetails),
			"user_input":        contentText(inv.UserContent.GenAI()),
			"tool_usage":        toolSteps(inv),
			"rubrics":           formatRubrics(rubrics),
		})}
	}
	return e.j.summarize(e.j.judge(ctx, tasks)), nil
}
