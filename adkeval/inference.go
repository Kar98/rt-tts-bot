package adkeval

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Defaults adk-python uses when an eval case has no session_input.
const (
	defaultAppName = "EvaluationGenerator"
	defaultUserID  = "test_user_id"
)

// runInference replays c's user turns against a in a fresh session and
// returns what the agent did on each turn, plus the session afterwards.
func runInference(ctx context.Context, a agent.Agent, c EvalCase) ([]Invocation, *SessionDetails, error) {
	if len(c.Conversation) == 0 {
		return nil, nil, errors.New("conversation_scenario is not supported")
	}
	appName, userID := defaultAppName, defaultUserID
	var state map[string]any
	if in := c.SessionInput; in != nil {
		if in.AppName != "" {
			appName = in.AppName
		}
		if in.UserID != "" {
			userID = in.UserID
		}
		state = maps.Clone(in.State)
	}

	// Each case gets its own recorder and runner, so cases running at the same
	// time never share recorded app details.
	rec := &appDetailsRecorder{}
	p, err := plugin.New(plugin.Config{Name: "adkeval_app_details", BeforeModelCallback: rec.beforeModel})
	if err != nil {
		return nil, nil, err
	}
	sessions := session.InMemoryService()
	created, err := sessions.Create(ctx, &session.CreateRequest{AppName: appName, UserID: userID, State: state})
	if err != nil {
		return nil, nil, err
	}
	sessionID := created.Session.ID()
	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          a,
		SessionService: sessions,
		PluginConfig:   runner.PluginConfig{Plugins: []*plugin.Plugin{p}},
	})
	if err != nil {
		return nil, nil, err
	}

	var invs []Invocation
	for i, want := range c.Conversation {
		start := time.Now()
		var events []*session.Event
		for ev, err := range r.Run(ctx, userID, sessionID, want.UserContent.GenAI(), agent.RunConfig{}) {
			if err != nil {
				return nil, nil, fmt.Errorf("turn %d: %w", i+1, err)
			}
			events = append(events, ev)
		}
		inv := toInvocation(want.UserContent, events)
		inv.CreationTimestamp = float64(start.UnixMicro()) / 1e6
		inv.Duration = ptr(math.Round(time.Since(start).Seconds()*1000) / 1000)
		inv.AppDetails = rec.snapshot()
		invs = append(invs, inv)
	}

	got, err := sessions.Get(ctx, &session.GetRequest{AppName: appName, UserID: userID, SessionID: sessionID})
	if err != nil {
		return nil, nil, err
	}
	s := got.Session
	details := &SessionDetails{
		ID:             s.ID(),
		AppName:        s.AppName(),
		UserID:         s.UserID(),
		State:          maps.Collect(s.State().All()),
		Events:         []any{},
		LastUpdateTime: float64(s.LastUpdateTime().UnixMicro()) / 1e6,
	}
	return invs, details, nil
}

// toInvocation turns one turn's events into an Invocation, as adk-python's
// convert_events_to_eval_invocations does. The last final-response event
// becomes the final response; a text-less one doesn't replace one with text.
// Every event with a tool call, tool response or text becomes an invocation
// event; the final-response event keeps its entry but without content, unless
// it holds tool calls, so its text isn't counted twice.
func toInvocation(userContent *Content, events []*session.Event) Invocation {
	inv := Invocation{UserContent: userContent, IntermediateData: &IntermediateData{}}
	final := -1
	for i, ev := range events {
		if ev.Partial || ev.Content == nil || len(ev.Content.Parts) == 0 {
			continue
		}
		if inv.InvocationID == "" {
			inv.InvocationID = ev.InvocationID
		}
		if ev.IsFinalResponse() && (final < 0 || hasText(ev.Content) || !hasText(events[final].Content)) {
			final = i
		}
	}

	for i, ev := range events {
		if ev.Partial || ev.Content == nil || !worthRecording(ev.Content) {
			continue
		}
		content := ev.Content
		if i == final && !hasFunctionCall(content) {
			content = nil
		}
		inv.IntermediateData.InvocationEvents = append(inv.IntermediateData.InvocationEvents, InvocationEvent{
			Author:        ev.Author,
			Content:       NewContent(content),
			UsageMetadata: (*UsageMetadata)(ev.UsageMetadata),
			ModelVersion:  ev.ModelVersion,
		})
	}
	if final >= 0 {
		inv.FinalResponse = NewContent(events[final].Content)
	}
	return inv
}

func worthRecording(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p != nil && (p.FunctionCall != nil || p.FunctionResponse != nil || p.Text != "" || p.InlineData != nil) {
			return true
		}
	}
	return false
}

func hasText(c *genai.Content) bool { return contentText(c) != "" }

func hasFunctionCall(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p != nil && p.FunctionCall != nil {
			return true
		}
	}
	return false
}

// appDetailsRecorder records each agent's instruction and tools from the
// requests it sends to its model. The rubric judges read them.
type appDetailsRecorder struct {
	mu     sync.Mutex
	agents map[string]AgentDetails
}

func (r *appDetailsRecorder) beforeModel(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	d := AgentDetails{Name: ctx.AgentName(), ToolDeclarations: []*Tool{}}
	if cfg := req.Config; cfg != nil {
		d.Instructions = strings.TrimSpace(contentText(cfg.SystemInstruction))
		for _, t := range cfg.Tools {
			d.ToolDeclarations = append(d.ToolDeclarations, (*Tool)(t))
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.agents == nil {
		r.agents = map[string]AgentDetails{}
	}
	r.agents[d.Name] = d
	return nil, nil
}

// snapshot returns the agents recorded so far, or nil if none.
func (r *appDetailsRecorder) snapshot() *AppDetails {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.agents) == 0 {
		return nil
	}
	return &AppDetails{AgentDetails: maps.Clone(r.agents)}
}
