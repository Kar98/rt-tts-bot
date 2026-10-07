// Package adkeval runs ADK Go agents against adk-python eval sets and scores
// them. It reads adk-python's *.evalset.json and test_config.json files and
// writes its *.evalset_result.json format, so the same files work with both.
//
// Supported metrics: rubric_based_final_response_quality_v1,
// rubric_based_tool_use_quality_v1,
// rubric_based_multi_turn_trajectory_quality_v1 and tool_trajectory_avg_score.
package adkeval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"google.golang.org/genai"
)

// EvalSet is adk-python's EvalSet.
type EvalSet struct {
	EvalSetID         string     `json:"eval_set_id"`
	Name              string     `json:"name,omitempty"`
	Description       string     `json:"description,omitempty"`
	EvalCases         []EvalCase `json:"eval_cases"`
	CreationTimestamp float64    `json:"creation_timestamp,omitempty"`
}

// EvalCase is adk-python's EvalCase. Exactly one of Conversation and
// ConversationScenario is set. Scenarios are kept so files round-trip, but
// cannot be run.
type EvalCase struct {
	EvalID               string          `json:"eval_id"`
	Conversation         []Invocation    `json:"conversation,omitempty"`
	ConversationScenario json.RawMessage `json:"conversation_scenario,omitempty"`
	SessionInput         *SessionInput   `json:"session_input,omitempty"`
	CreationTimestamp    float64         `json:"creation_timestamp,omitempty"`
	Rubrics              []Rubric        `json:"rubrics,omitempty"`
	FinalSessionState    map[string]any  `json:"final_session_state,omitempty"`
	// Extra holds keys this package doesn't know. adk-python allows them on
	// EvalCase, so they are written back unchanged.
	Extra map[string]json.RawMessage `json:"-"`
}

type evalCaseJSON EvalCase

func (c EvalCase) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(evalCaseJSON(c), c.Extra)
}

func (c *EvalCase) UnmarshalJSON(b []byte) error {
	extra, err := unmarshalWithExtra(b, (*evalCaseJSON)(c))
	c.Extra = extra
	return err
}

// SessionInput is the session the agent starts each case with.
type SessionInput struct {
	AppName   string         `json:"app_name"`
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id,omitempty"`
	State     map[string]any `json:"state,omitempty"`
	// Extra holds unknown keys, as on EvalCase.
	Extra map[string]json.RawMessage `json:"-"`
}

type sessionInputJSON SessionInput

func (s SessionInput) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(sessionInputJSON(s), s.Extra)
}

func (s *SessionInput) UnmarshalJSON(b []byte) error {
	extra, err := unmarshalWithExtra(b, (*sessionInputJSON)(s))
	s.Extra = extra
	return err
}

// Invocation is one user turn and the agent's reply to it. In an eval set it
// is the expected turn; in a result it is also what the agent did.
type Invocation struct {
	InvocationID      string            `json:"invocation_id,omitempty"`
	UserContent       *Content          `json:"user_content"`
	FinalResponse     *Content          `json:"final_response,omitempty"`
	IntermediateData  *IntermediateData `json:"intermediate_data,omitempty"`
	CreationTimestamp float64           `json:"creation_timestamp,omitempty"`
	// Duration is the wall-clock time of the turn in seconds.
	Duration   *float64    `json:"duration,omitempty"`
	Rubrics    []Rubric    `json:"rubrics,omitempty"`
	AppDetails *AppDetails `json:"app_details,omitempty"`
}

// IntermediateData is what happened between the user message and the final
// response. adk-python has two shapes for it: the current invocation_events
// list, and a legacy one with tool_uses, tool_responses and
// intermediate_responses. Legacy says which one this holds.
type IntermediateData struct {
	InvocationEvents []InvocationEvent

	Legacy                bool
	ToolUses              []*FunctionCall
	ToolResponses         []*FunctionResponse
	IntermediateResponses []IntermediateResponse
}

type invocationEventsJSON struct {
	InvocationEvents []InvocationEvent `json:"invocation_events"`
}

type legacyIntermediateJSON struct {
	ToolUses              []*FunctionCall        `json:"tool_uses"`
	ToolResponses         []*FunctionResponse    `json:"tool_responses"`
	IntermediateResponses []IntermediateResponse `json:"intermediate_responses"`
}

func (d IntermediateData) MarshalJSON() ([]byte, error) {
	if d.Legacy {
		return json.Marshal(legacyIntermediateJSON{
			ToolUses:              orEmpty(d.ToolUses),
			ToolResponses:         orEmpty(d.ToolResponses),
			IntermediateResponses: orEmpty(d.IntermediateResponses),
		})
	}
	return json.Marshal(invocationEventsJSON{orEmpty(d.InvocationEvents)})
}

func (d *IntermediateData) UnmarshalJSON(b []byte) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	if _, ok := keys["invocation_events"]; ok {
		var v invocationEventsJSON
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*d = IntermediateData{InvocationEvents: v.InvocationEvents}
		return nil
	}
	var v legacyIntermediateJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*d = IntermediateData{
		Legacy:                true,
		ToolUses:              v.ToolUses,
		ToolResponses:         v.ToolResponses,
		IntermediateResponses: v.IntermediateResponses,
	}
	return nil
}

// InvocationEvent is one agent event within a turn.
type InvocationEvent struct {
	Author        string         `json:"author"`
	Content       *Content       `json:"content"`
	UsageMetadata *UsageMetadata `json:"usage_metadata,omitempty"`
	ModelVersion  string         `json:"model_version,omitempty"`
}

// IntermediateResponse is text an agent produced before the final response,
// in the legacy shape. It is written as the JSON array [author, parts].
type IntermediateResponse struct {
	Author string
	Parts  []*Part
}

func (r IntermediateResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{r.Author, orEmpty(r.Parts)})
}

func (r *IntermediateResponse) UnmarshalJSON(b []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) != 2 {
		return fmt.Errorf("intermediate_responses entry has %d elements, want [author, parts]", len(pair))
	}
	if err := json.Unmarshal(pair[0], &r.Author); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &r.Parts)
}

// Rubric is a yes/no property an LLM judge checks the agent against.
type Rubric struct {
	RubricID      string        `json:"rubric_id"`
	RubricContent RubricContent `json:"rubric_content"`
	Description   string        `json:"description,omitempty"`
	// Type limits which metric uses a case or invocation rubric, e.g.
	// FINAL_RESPONSE_QUALITY. Criterion rubrics ignore it.
	Type string `json:"type,omitempty"`
}

type RubricContent struct {
	TextProperty *string `json:"text_property"`
}

// Text returns the rubric's property text.
func (r Rubric) Text() string {
	if r.RubricContent.TextProperty == nil {
		return ""
	}
	return *r.RubricContent.TextProperty
}

// AppDetails records each agent's instructions and tools during a turn. The
// rubric judges read it.
type AppDetails struct {
	AgentDetails map[string]AgentDetails `json:"agent_details"`
}

type AgentDetails struct {
	Name             string  `json:"name"`
	Instructions     string  `json:"instructions"`
	ToolDeclarations []*Tool `json:"tool_declarations"`
}

// LoadEvalSet reads and validates an adk-python eval set file.
func LoadEvalSet(path string) (*EvalSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s EvalSet
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Save writes s as indented JSON.
func (s *EvalSet) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (s *EvalSet) validate() error {
	if s.EvalSetID == "" {
		return errors.New("eval_set_id is empty")
	}
	for i, c := range s.EvalCases {
		if c.EvalID == "" {
			return fmt.Errorf("eval case %d: eval_id is empty", i)
		}
		hasScenario := len(c.ConversationScenario) > 0 && string(c.ConversationScenario) != "null"
		if (len(c.Conversation) > 0) == hasScenario {
			return fmt.Errorf("eval case %s: needs exactly one of conversation and conversation_scenario", c.EvalID)
		}
		for j, inv := range c.Conversation {
			if inv.UserContent == nil {
				return fmt.Errorf("eval case %s: invocation %d has no user_content", c.EvalID, j)
			}
		}
	}
	return nil
}

// toolCalls returns the tool calls made during inv, from either shape of
// intermediate data.
func (inv Invocation) toolCalls() []*genai.FunctionCall {
	var calls []*genai.FunctionCall
	d := inv.IntermediateData
	if d == nil {
		return nil
	}
	if d.Legacy {
		for _, c := range d.ToolUses {
			calls = append(calls, (*genai.FunctionCall)(c))
		}
		return calls
	}
	for _, ev := range d.InvocationEvents {
		if ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.FunctionCall != nil {
				calls = append(calls, p.FunctionCall)
			}
		}
	}
	return calls
}

// toolResponses returns the tool responses from inv, from either shape of
// intermediate data.
func (inv Invocation) toolResponses() []*genai.FunctionResponse {
	var resps []*genai.FunctionResponse
	d := inv.IntermediateData
	if d == nil {
		return nil
	}
	if d.Legacy {
		for _, r := range d.ToolResponses {
			resps = append(resps, (*genai.FunctionResponse)(r))
		}
		return resps
	}
	for _, ev := range d.InvocationEvents {
		if ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.FunctionResponse != nil {
				resps = append(resps, p.FunctionResponse)
			}
		}
	}
	return resps
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// marshalWithExtra marshals v, then adds the keys in extra that v doesn't
// already write.
func marshalWithExtra(v any, extra map[string]json.RawMessage) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return b, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	for k, val := range extra {
		if _, ok := obj[k]; !ok {
			obj[k] = val
		}
	}
	return json.Marshal(obj)
}

// unmarshalWithExtra unmarshals b into v, a pointer to a struct, and returns
// the keys that match none of its json tags.
func unmarshalWithExtra(b []byte, v any) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(b, v); err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	t := reflect.TypeOf(v).Elem()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		delete(obj, name)
	}
	if len(obj) == 0 {
		return nil, nil
	}
	return obj, nil
}
