package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LangfuseClient calls the Langfuse public REST API. Traces go through OTLP
// (see ConfigureLangfuse); this covers what OTLP can't send: datasets and
// evaluator setup.
type LangfuseClient struct {
	cfg  LangfuseConfig
	http *http.Client
}

func NewLangfuseClient(cfg LangfuseConfig) *LangfuseClient {
	return &LangfuseClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// UpsertDataset creates the dataset, or leaves it as is if it exists, and
// returns its id.
func (c *LangfuseClient) UpsertDataset(ctx context.Context, name, description string) (string, error) {
	var resp struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/api/public/v2/datasets", map[string]any{
		"name":        name,
		"description": description,
	}, &resp)
	return resp.ID, err
}

// UpsertDatasetItem creates or replaces the item with this id. Ids are
// project-wide in Langfuse, so callers should prefix them with the dataset name.
func (c *LangfuseClient) UpsertDatasetItem(ctx context.Context, dataset, id string, input, expectedOutput any) error {
	return c.do(ctx, http.MethodPost, "/api/public/dataset-items", map[string]any{
		"datasetName":    dataset,
		"id":             id,
		"input":          input,
		"expectedOutput": expectedOutput,
	}, nil)
}

// NamedID is the part of a Langfuse evaluator or evaluation rule we match on.
type NamedID struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *LangfuseClient) ListEvaluators(ctx context.Context) ([]NamedID, error) {
	return c.list(ctx, "/api/public/v2/evaluators")
}

func (c *LangfuseClient) ListEvaluationRules(ctx context.Context) ([]NamedID, error) {
	return c.list(ctx, "/api/public/v2/evaluation-rules")
}

// CountLLMConnections returns how many LLM connections the project has.
// Evaluators need one to run.
func (c *LangfuseClient) CountLLMConnections(ctx context.Context) (int, error) {
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/api/public/llm-connections", nil, &resp)
	return len(resp.Data), err
}

// CreateEvaluator takes a CreateEvaluatorRequest from the Langfuse API.
func (c *LangfuseClient) CreateEvaluator(ctx context.Context, req any) (string, error) {
	var resp NamedID
	err := c.do(ctx, http.MethodPost, "/api/public/v2/evaluators", req, &resp)
	return resp.ID, err
}

// CreateEvaluationRule takes a CreateEvaluationRuleRequest from the Langfuse API.
func (c *LangfuseClient) CreateEvaluationRule(ctx context.Context, req any) (string, error) {
	var resp NamedID
	err := c.do(ctx, http.MethodPost, "/api/public/v2/evaluation-rules", req, &resp)
	return resp.ID, err
}

// list reads every page of a cursor-paginated v2 list endpoint.
func (c *LangfuseClient) list(ctx context.Context, path string) ([]NamedID, error) {
	var all []NamedID
	cursor := ""
	for {
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var resp struct {
			Data []NamedID `json:"data"`
			Meta struct {
				Cursor string `json:"cursor"`
			} `json:"meta"`
		}
		if err := c.do(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		if resp.Meta.Cursor == "" || len(resp.Data) == 0 {
			return all, nil
		}
		cursor = resp.Meta.Cursor
	}
}

// do sends body as JSON, if not nil, and decodes the response into out, if not nil.
func (c *LangfuseClient) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.Host, "/")+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.cfg.PublicKey, c.cfg.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("langfuse %s %s: %s: %s", method, path, resp.Status, msg)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
