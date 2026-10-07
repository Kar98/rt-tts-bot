package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LangfuseClient calls the Langfuse public REST API. Traces go through OTLP
// (see ConfigureLangfuse); this covers what OTLP can't send: datasets and
// scores.
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

// Score is a numeric score on a trace.
type Score struct {
	// ID makes the call idempotent: a score with the same ID replaces the old
	// one.
	ID      string
	TraceID string
	Name    string
	Value   float64
	Comment string
}

// CreateScore creates or replaces a numeric score.
func (c *LangfuseClient) CreateScore(ctx context.Context, s Score) error {
	body := map[string]any{
		"id":       s.ID,
		"traceId":  s.TraceID,
		"name":     s.Name,
		"value":    s.Value,
		"dataType": "NUMERIC",
	}
	if s.Comment != "" {
		body["comment"] = s.Comment
	}
	return c.do(ctx, http.MethodPost, "/api/public/scores", body, nil)
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
