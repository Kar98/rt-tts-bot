// Package evals runs an agent over an eval set and records each run in
// Langfuse as an experiment. ADK Go has no eval framework (its eval REST routes
// return 501 and point to adk-python), so this fills the gap.
//
// Scoring happens in Langfuse: evaluators attached to the dataset score each
// experiment item after its trace arrives.
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// EvalSet is a named list of cases, loaded from JSON.
type EvalSet struct {
	ID          string `json:"eval_set_id"`
	Description string `json:"description,omitempty"`
	Cases       []Case `json:"eval_cases"`
}

// Case is one agent run to score.
type Case struct {
	ID string `json:"eval_id"`
	// Input is the user message sent to the agent.
	Input string `json:"input"`
	// State is the session state the agent starts with.
	State map[string]any `json:"session_state,omitempty"`
	// Context is what the evaluators treat as true, such as the chat the
	// response is based on.
	Context string `json:"context,omitempty"`
	// Reference is an optional example of a good response. It becomes the
	// dataset item's expected output.
	Reference string `json:"reference,omitempty"`
}

func LoadEvalSet(path string) (*EvalSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s EvalSet
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.ID == "" {
		return nil, fmt.Errorf("%s: eval_set_id is empty", path)
	}
	for i, c := range s.Cases {
		if c.ID == "" || c.Input == "" {
			return nil, fmt.Errorf("%s: case %d needs eval_id and input", path, i)
		}
	}
	return &s, nil
}

// RunAgent runs a in a fresh in-memory session and returns its final text.
func RunAgent(ctx context.Context, a agent.Agent, c Case) (string, error) {
	const appName, userID = "evals", "eval_user"
	sessions := session.InMemoryService()
	created, err := sessions.Create(ctx, &session.CreateRequest{AppName: appName, UserID: userID, State: c.State})
	if err != nil {
		return "", err
	}
	r, err := runner.New(runner.Config{AppName: appName, Agent: a, SessionService: sessions})
	if err != nil {
		return "", err
	}

	var out string
	msg := genai.NewContentFromText(c.Input, genai.RoleUser)
	for ev, err := range r.Run(ctx, userID, created.Session.ID(), msg, agent.RunConfig{}) {
		if err != nil {
			return "", err
		}
		if ev.IsFinalResponse() {
			if t := text(ev.Content); t != "" {
				out = t
			}
		}
	}
	if out == "" {
		return "", fmt.Errorf("agent returned no text")
	}
	return out, nil
}

func text(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range c.Parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
