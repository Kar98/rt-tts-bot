package main

import (
	"bufio"
	"bytes"
	"cmp"
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

// AgentClient sends a message to the agent and returns its reply. An empty
// sessionID starts a new session; the session used is returned.
type AgentClient interface {
	Send(ctx context.Context, userID, sessionID, message string) (reply Reply, newSessionID string, err error)
}

// Reply is what the frontend shows for one agent run.
type Reply struct {
	Text string
	// Summary is the chat_summariser output, if the agent called it this run.
	Summary string
}

// summariserName is the chat_summariser tool name, agents.SummariserName.
const summariserName = "chat_summariser"

// event is the subset of an ADK event the frontend reads. The ADK REST API
// uses camelCase and Agent Engine snake_case; the top-level names are the same
// in both.
type event struct {
	Author  string `json:"author"`
	Partial bool   `json:"partial"`
	Content *struct {
		Parts []part `json:"parts"`
	} `json:"content"`
	Error string `json:"error"`
}

// part is a genai.Part cut down to what we read. genai.Part only decodes
// camelCase, so Agent Engine's function_response needs its own field.
type part struct {
	Text                  string            `json:"text"`
	Thought               bool              `json:"thought"`
	FunctionResponse      *functionResponse `json:"functionResponse"`
	FunctionResponseSnake *functionResponse `json:"function_response"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

func (e event) text() string {
	if e.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range e.Content.Parts {
		if !p.Thought {
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

// lastSummary returns the result of the last chat_summariser call, or "".
// agenttool returns the sub-agent's text as {"result": text}.
func lastSummary(events []event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Content == nil {
			continue
		}
		parts := events[i].Content.Parts
		for j := len(parts) - 1; j >= 0; j-- {
			fr := cmp.Or(parts[j].FunctionResponse, parts[j].FunctionResponseSnake)
			if fr == nil || fr.Name != summariserName {
				continue
			}
			if s, ok := fr.Response["result"].(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func parseReply(events []event, author string) (Reply, error) {
	text, err := finalReply(events, author)
	return Reply{Text: text, Summary: lastSummary(events)}, err
}

// adkrestClient talks to a local agent started with "web api".
type adkrestClient struct {
	base    string
	appName string
	http    *http.Client
}

func (c *adkrestClient) Send(ctx context.Context, userID, sessionID, message string) (Reply, string, error) {
	if sessionID == "" {
		var sess struct {
			ID string `json:"id"`
		}
		u := fmt.Sprintf("%s/apps/%s/users/%s/sessions", c.base, url.PathEscape(c.appName), url.PathEscape(userID))
		if err := postJSON(ctx, c.http, u, map[string]any{}, &sess); err != nil {
			return Reply{}, "", fmt.Errorf("create session: %w", err)
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
		return Reply{}, sessionID, fmt.Errorf("run: %w", err)
	}
	reply, err := parseReply(events, c.appName)
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

func (c *agentEngineClient) Send(ctx context.Context, userID, sessionID, message string) (Reply, string, error) {
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
			return Reply{}, "", fmt.Errorf("create session: %w", err)
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
		return Reply{}, sessionID, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+":streamQuery?alt=sse", bytes.NewReader(body))
	if err != nil {
		return Reply{}, sessionID, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Reply{}, sessionID, fmt.Errorf("stream query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Reply{}, sessionID, fmt.Errorf("stream query: %s: %s", resp.Status, b)
	}

	events, err := readEventStream(resp.Body)
	if err != nil {
		return Reply{}, sessionID, fmt.Errorf("stream query: %w", err)
	}
	reply, err := parseReply(events, c.appName)
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
