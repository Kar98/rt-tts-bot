package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/oauth2/google"
	"google.golang.org/genai"
)

// AgentClient sends a message to the agent and returns its final reply. An
// empty sessionID starts a new session; the session used is returned.
type AgentClient interface {
	Summarise(ctx context.Context, userID, sessionID, message string) (reply, newSessionID string, err error)
}

// event is the subset of an ADK event the frontend reads. Both the ADK REST
// API (camelCase) and Agent Engine (snake_case) use these field names.
type event struct {
	Author  string         `json:"author"`
	Partial bool           `json:"partial"`
	Content *genai.Content `json:"content"`
	Error   string         `json:"error"`
}

func (e event) text() string {
	if e.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range e.Content.Parts {
		if p != nil && !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// finalReply returns the text of the last complete event authored by author.
func finalReply(events []event, author string) (string, error) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Error != "" {
			return "", fmt.Errorf("agent error: %s", e.Error)
		}
		if e.Author == author && !e.Partial {
			if t := e.text(); t != "" {
				return t, nil
			}
		}
	}
	return "", errors.New("agent returned no reply")
}

// adkrestClient talks to a local agent started with "web api".
type adkrestClient struct {
	base    string
	appName string
	http    *http.Client
}

func (c *adkrestClient) Summarise(ctx context.Context, userID, sessionID, message string) (string, string, error) {
	if sessionID == "" {
		var sess struct {
			ID string `json:"id"`
		}
		u := fmt.Sprintf("%s/apps/%s/users/%s/sessions", c.base, url.PathEscape(c.appName), url.PathEscape(userID))
		if err := postJSON(ctx, c.http, u, map[string]any{}, &sess); err != nil {
			return "", "", fmt.Errorf("create session: %w", err)
		}
		sessionID = sess.ID
	}

	var events []event
	err := postJSON(ctx, c.http, c.base+"/run", map[string]any{
		"appName":    c.appName,
		"userId":     userID,
		"sessionId":  sessionID,
		"newMessage": genai.NewContentFromText(message, genai.RoleUser),
	}, &events)
	if err != nil {
		return "", sessionID, fmt.Errorf("run: %w", err)
	}
	reply, err := finalReply(events, c.appName)
	return reply, sessionID, err
}

// agentEngineClient talks to an agent deployed on Vertex AI Agent Engine.
type agentEngineClient struct {
	base    string // https://{loc}-aiplatform.googleapis.com/v1/projects/{p}/locations/{loc}/reasoningEngines/{id}
	appName string
	http    *http.Client
}

func newAgentEngineClient(ctx context.Context, project, location, id, appName string) (*agentEngineClient, error) {
	hc, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("google credentials: %w", err)
	}
	return &agentEngineClient{
		base:    fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/reasoningEngines/%s", location, project, location, id),
		appName: appName,
		http:    hc,
	}, nil
}

func (c *agentEngineClient) Summarise(ctx context.Context, userID, sessionID, message string) (string, string, error) {
	if sessionID == "" {
		var resp struct {
			Output struct {
				ID string `json:"id"`
			} `json:"output"`
		}
		err := postJSON(ctx, c.http, c.base+":query", map[string]any{
			"class_method": "async_create_session",
			"input":        map[string]any{"user_id": userID},
		}, &resp)
		if err != nil {
			return "", "", fmt.Errorf("create session: %w", err)
		}
		sessionID = resp.Output.ID
	}

	body, err := json.Marshal(map[string]any{
		"class_method": "async_stream_query",
		"input": map[string]any{
			"user_id":    userID,
			"session_id": sessionID,
			"message":    message,
		},
	})
	if err != nil {
		return "", sessionID, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+":streamQuery?alt=sse", bytes.NewReader(body))
	if err != nil {
		return "", sessionID, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", sessionID, fmt.Errorf("stream query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", sessionID, fmt.Errorf("stream query: %s: %s", resp.Status, b)
	}

	events, err := readEventStream(resp.Body)
	if err != nil {
		return "", sessionID, fmt.Errorf("stream query: %w", err)
	}
	reply, err := finalReply(events, c.appName)
	return reply, sessionID, err
}

// readEventStream parses newline-delimited JSON events, also accepting SSE
// "data: " prefixed lines.
func readEventStream(r io.Reader) ([]event, error) {
	var events []event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("decode event: %w", err)
		}
		events = append(events, e)
	}
	return events, sc.Err()
}

func postJSON(ctx context.Context, hc *http.Client, u string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, out)
}
