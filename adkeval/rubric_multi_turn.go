package adkeval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genai"
)

//go:embed prompts/rubric_based_multi_turn_trajectory_quality_v1.txt
var multiTurnPromptFile string

var multiTurnPrompt = loadPrompt(multiTurnPromptFile)

// multiTurnTrajectoryEvaluator is
// rubric_based_multi_turn_trajectory_quality_v1: it judges the whole
// conversation once, on the last turn, against TRAJECTORY_QUALITY rubrics.
// Earlier turns are NOT_EVALUATED.
type multiTurnTrajectoryEvaluator struct{ j *rubricJudge }

func newMultiTurnTrajectoryEvaluator(ctx context.Context, c Criterion, judges *judgeModels) (Evaluator, error) {
	j, err := newRubricJudge(ctx, c, judges, "TRAJECTORY_QUALITY")
	if err != nil {
		return nil, err
	}
	return &multiTurnTrajectoryEvaluator{j}, nil
}

func (e *multiTurnTrajectoryEvaluator) Evaluate(ctx context.Context, actual, _ []Invocation) (MetricResult, error) {
	if len(actual) == 0 {
		return MetricResult{Status: StatusNotEvaluated}, nil
	}
	last := actual[len(actual)-1]
	rubrics, err := e.j.effectiveRubrics(last)
	if err != nil {
		return MetricResult{}, err
	}
	props, err := formatProperties(rubrics)
	if err != nil {
		return MetricResult{}, err
	}
	instructions, tools := agentDefinitions(actual)
	prompt := fillPrompt(multiTurnPrompt, map[string]string{
		"agent_instructions":     instructions,
		"agent_tool_definitions": tools,
		"user_agent_dialogue":    dialogue(actual),
		"properties":             props,
	})

	judged := e.j.summarize(e.j.judge(ctx, []judgeTask{{turn: len(actual), prompt: prompt, rubrics: rubrics}}))
	perInvocation := make([]InvocationResult, len(actual))
	for i := range len(actual) - 1 {
		perInvocation[i] = InvocationResult{Status: StatusNotEvaluated}
	}
	perInvocation[len(actual)-1] = judged.PerInvocation[0]
	judged.PerInvocation = perInvocation
	return judged, nil
}

// formatProperties lists rubrics as the JSON array the multi-turn prompt
// expects.
func formatProperties(rubrics []Rubric) (string, error) {
	type property struct {
		ID       string `json:"id"`
		Property string `json:"property"`
		Type     string `json:"type,omitempty"`
	}
	props := make([]property, len(rubrics))
	for i, r := range rubrics {
		props[i] = property{ID: r.RubricID, Property: r.Text(), Type: r.Type}
	}
	b, err := json.MarshalIndent(props, "", "  ")
	return string(b), err
}

// dialogue writes the conversation one line per message, tool call and tool
// output, in the format of adk-python's _assemble_dialogue_history.
func dialogue(invs []Invocation) string {
	var lines []string
	for i, inv := range invs {
		turn := i + 1
		if t := joinedText(inv.UserContent.GenAI()); t != "" {
			lines = append(lines, fmt.Sprintf("USER TURN %d: %s", turn, t))
		}
		agentName := "agent"
		if d := inv.IntermediateData; d != nil && !d.Legacy {
			if len(d.InvocationEvents) > 0 {
				agentName = d.InvocationEvents[0].Author
			}
			for _, ev := range d.InvocationEvents {
				role := "AGENT (" + ev.Author + ")"
				if strings.EqualFold(ev.Author, "user") {
					role = "USER"
				}
				c := ev.Content.GenAI()
				if t := joinedText(c); t != "" {
					lines = append(lines, fmt.Sprintf("%s TURN %d: %s", role, turn, t))
				}
				if c == nil {
					continue
				}
				for _, p := range c.Parts {
					if p.FunctionCall != nil {
						lines = append(lines, fmt.Sprintf("%s TURN %d (tool call): %s(%s)",
							role, turn, p.FunctionCall.Name, jsonOrEmpty(p.FunctionCall.Args)))
					}
					if p.FunctionResponse != nil {
						lines = append(lines, fmt.Sprintf("%s TURN %d (tool output): %s -> %s",
							role, turn, p.FunctionResponse.Name, jsonOrEmpty(p.FunctionResponse.Response)))
					}
				}
			}
		}
		if t := joinedText(inv.FinalResponse.GenAI()); t != "" {
			lines = append(lines, fmt.Sprintf("AGENT (%s) TURN %d: %s", agentName, turn, t))
		}
	}
	return strings.Join(lines, "\n")
}

// joinedText joins text parts with spaces, as the dialogue format does.
func joinedText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var texts []string
	for _, p := range c.Parts {
		if p != nil && p.Text != "" && !p.Thought {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, " ")
}

func jsonOrEmpty(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// agentDefinitions returns every recorded agent's instructions and tool
// names across the conversation, without repeats.
func agentDefinitions(invs []Invocation) (instructions, tools string) {
	var instr, toolLines []string
	for _, inv := range invs {
		if inv.AppDetails == nil {
			continue
		}
		names := make([]string, 0, len(inv.AppDetails.AgentDetails))
		for name := range inv.AppDetails.AgentDetails {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			a := inv.AppDetails.AgentDetails[name]
			instr = appendNew(instr, fmt.Sprintf("Agent %s Instructions:\n%s", name, a.Instructions))
			toolLines = appendNew(toolLines, "Agent: "+name)
			for _, t := range a.ToolDeclarations {
				for _, f := range t.FunctionDeclarations {
					toolLines = appendNew(toolLines, fmt.Sprintf("- %s: %s", f.Name, f.Description))
				}
			}
		}
	}
	return strings.Join(instr, "\n\n"), strings.Join(toolLines, "\n")
}

func appendNew(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
