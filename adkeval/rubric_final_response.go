package adkeval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed prompts/rubric_based_final_response_quality_v1.txt
var finalResponsePromptFile string

var finalResponsePrompt = loadPrompt(finalResponsePromptFile)

// finalResponseQualityEvaluator is rubric_based_final_response_quality_v1:
// it judges each turn's final response against FINAL_RESPONSE_QUALITY
// rubrics.
type finalResponseQualityEvaluator struct{ j *rubricJudge }

func newFinalResponseQualityEvaluator(ctx context.Context, c Criterion, judges *judgeModels) (Evaluator, error) {
	j, err := newRubricJudge(ctx, c, judges, "FINAL_RESPONSE_QUALITY")
	if err != nil {
		return nil, err
	}
	return &finalResponseQualityEvaluator{j}, nil
}

func (e *finalResponseQualityEvaluator) Evaluate(ctx context.Context, actual, _ []Invocation) (MetricResult, error) {
	tasks := make([]judgeTask, len(actual))
	for i, inv := range actual {
		rubrics, err := e.j.effectiveRubrics(inv)
		if err != nil {
			return MetricResult{}, err
		}
		tasks[i] = judgeTask{rubrics: rubrics, prompt: fillPrompt(finalResponsePrompt, map[string]string{
			"developer_instructions": developerInstructions(inv),
			"tool_declarations":      toolDeclarations(inv.AppDetails),
			"user_input":             contentText(inv.UserContent.GenAI()),
			"response_steps":         toolSteps(inv),
			"grounding_metadata":     "No grounding metadata was provided.",
			"final_response":         contentText(inv.FinalResponse.GenAI()),
			"rubrics":                formatRubrics(rubrics),
		})}
	}
	return e.j.summarize(e.j.judge(ctx, tasks)), nil
}

// developerInstructions returns the instruction of the agent that handled inv:
// the author of its first event, or else the first recorded agent.
func developerInstructions(inv Invocation) string {
	d := inv.AppDetails
	if d == nil || len(d.AgentDetails) == 0 {
		return ""
	}
	if inv.IntermediateData != nil && len(inv.IntermediateData.InvocationEvents) > 0 {
		if a, ok := d.AgentDetails[inv.IntermediateData.InvocationEvents[0].Author]; ok {
			return a.Instructions
		}
	}
	names := make([]string, 0, len(d.AgentDetails))
	for name := range d.AgentDetails {
		names = append(names, name)
	}
	slices.Sort(names)
	return d.AgentDetails[names[0]].Instructions
}

// toolDeclarations returns each agent's tools as JSON, keyed by agent name.
func toolDeclarations(d *AppDetails) string {
	if d == nil {
		return "Agent has no tools."
	}
	tools := map[string][]*Tool{}
	for name, a := range d.AgentDetails {
		tools[name] = orEmpty(a.ToolDeclarations)
	}
	b, err := json.MarshalIndent(map[string]any{"tool_declarations": tools}, "", "  ")
	if err != nil {
		return fmt.Sprintf("tool declarations could not be encoded: %v", err)
	}
	return string(b)
}

type toolStep struct {
	Step         int           `json:"step"`
	ToolCall     *FunctionCall `json:"tool_call"`
	ToolResponse any           `json:"tool_response"`
}

// toolSteps returns inv's tool calls, each paired with its response by id, as
// JSON.
func toolSteps(inv Invocation) string {
	calls := inv.toolCalls()
	if len(calls) == 0 {
		return "No intermediate steps were taken."
	}
	resps := inv.toolResponses()
	steps := make([]toolStep, len(calls))
	for i, c := range calls {
		var resp any = "None"
		for _, r := range resps {
			if r.ID == c.ID {
				resp = (*FunctionResponse)(r)
				break
			}
		}
		steps[i] = toolStep{Step: i, ToolCall: (*FunctionCall)(c), ToolResponse: resp}
	}
	b, err := json.MarshalIndent(map[string]any{"tool_calls_and_response": steps}, "", "  ")
	if err != nil {
		return fmt.Sprintf("tool calls could not be encoded: %v", err)
	}
	return string(b)
}
