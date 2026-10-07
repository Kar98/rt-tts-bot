package adkeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EvalStatus is adk-python's EvalStatus, written as its integer value.
type EvalStatus int

const (
	StatusPassed       EvalStatus = 1
	StatusFailed       EvalStatus = 2
	StatusNotEvaluated EvalStatus = 3
)

func (s EvalStatus) String() string {
	switch s {
	case StatusPassed:
		return "PASSED"
	case StatusFailed:
		return "FAILED"
	case StatusNotEvaluated:
		return "NOT_EVALUATED"
	}
	return fmt.Sprintf("EvalStatus(%d)", int(s))
}

// EvalSetResult is adk-python's EvalSetResult: one run of an eval set.
type EvalSetResult struct {
	EvalSetResultID   string           `json:"eval_set_result_id"`
	EvalSetResultName string           `json:"eval_set_result_name"`
	EvalSetID         string           `json:"eval_set_id"`
	EvalCaseResults   []EvalCaseResult `json:"eval_case_results"`
	CreationTimestamp float64          `json:"creation_timestamp"`
}

// EvalCaseResult is the result of one eval case.
type EvalCaseResult struct {
	// EvalSetFile is deprecated in adk-python and holds the eval set id.
	EvalSetFile     string     `json:"eval_set_file"`
	EvalSetID       string     `json:"eval_set_id"`
	EvalID          string     `json:"eval_id"`
	FinalEvalStatus EvalStatus `json:"final_eval_status"`
	// EvalMetricResults is deprecated in adk-python and always null.
	EvalMetricResults             []EvalMetricResult              `json:"eval_metric_results"`
	OverallEvalMetricResults      []EvalMetricResult              `json:"overall_eval_metric_results"`
	EvalMetricResultPerInvocation []EvalMetricResultPerInvocation `json:"eval_metric_result_per_invocation"`
	SessionID                     string                          `json:"session_id"`
	SessionDetails                *SessionDetails                 `json:"session_details"`
	UserID                        string                          `json:"user_id"`

	// Err says why the case failed or wasn't evaluated, if it was an error
	// rather than a score. It isn't written to the file.
	Err error `json:"-"`
}

// EvalMetricResultPerInvocation pairs one turn the agent took with the turn
// the eval set expected, and the metric results for it.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   Invocation         `json:"actual_invocation"`
	ExpectedInvocation *Invocation        `json:"expected_invocation"`
	EvalMetricResults  []EvalMetricResult `json:"eval_metric_results"`
}

// EvalMetricResult is one metric's result for a case or a turn.
type EvalMetricResult struct {
	MetricName         string                  `json:"metric_name"`
	Threshold          *float64                `json:"threshold"`
	Criterion          json.RawMessage         `json:"criterion"`
	CustomFunctionPath *string                 `json:"custom_function_path"`
	Score              *float64                `json:"score"`
	EvalStatus         EvalStatus              `json:"eval_status"`
	Details            EvalMetricResultDetails `json:"details"`

	// Err says why the metric wasn't evaluated. It isn't written to the file.
	Err error `json:"-"`
}

type EvalMetricResultDetails struct {
	RubricScores []RubricScore `json:"rubric_scores"`
	// TokenUsageDetails is always null: the efficiency metrics aren't
	// supported.
	TokenUsageDetails json.RawMessage `json:"token_usage_details"`
}

// RubricScore is the judge's verdict on one rubric.
type RubricScore struct {
	RubricID  string   `json:"rubric_id"`
	Rationale *string  `json:"rationale"`
	Score     *float64 `json:"score"`
}

// SessionDetails is the session a case ran in. Events are left empty.
type SessionDetails struct {
	ID             string         `json:"id"`
	AppName        string         `json:"app_name"`
	UserID         string         `json:"user_id"`
	State          map[string]any `json:"state"`
	Events         []any          `json:"events"`
	LastUpdateTime float64        `json:"last_update_time"`
}

// newResultID returns adk-python's eval_set_result_id and name for a run
// started at now.
func newResultID(appName, evalSetID string, now time.Time) (id, name string) {
	secs := float64(now.UnixMicro()) / 1e6
	id = appName + "_" + evalSetID + "_" + strconv.FormatFloat(secs, 'f', 6, 64)
	return id, strings.ReplaceAll(id, "/", "_")
}

// WriteResult writes r to dir as <eval_set_result_name>.evalset_result.json,
// the name adk-python uses, and returns the file's path.
func WriteResult(dir string, r *EvalSetResult) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, r.EvalSetResultName+".evalset_result.json")
	return path, os.WriteFile(path, append(b, '\n'), 0o644)
}

// LoadResult reads a result file written by WriteResult or adk-python.
func LoadResult(path string) (*EvalSetResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r EvalSetResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func ptr[T any](v T) *T { return &v }
