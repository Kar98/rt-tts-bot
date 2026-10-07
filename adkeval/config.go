package adkeval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// EvalConfig is adk-python's test_config.json: the metrics to run and their
// options.
type EvalConfig struct {
	// Criteria are in the order the file lists them.
	Criteria []Criterion
}

// Criterion is one metric with its threshold and options.
type Criterion struct {
	MetricName string
	Threshold  float64
	// Raw is the criterion as written in the file. Metrics read their options
	// from it, and results include it.
	Raw json.RawMessage
}

// LoadConfig reads a test_config.json. Unlike adk-python, a missing file is an
// error rather than a default set of metrics, and so is a metric this package
// doesn't support.
func LoadConfig(path string) (*EvalConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := parseConfig(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func parseConfig(b []byte) (*EvalConfig, error) {
	var top struct {
		Criteria json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	if len(top.Criteria) == 0 {
		return nil, errors.New("criteria is missing")
	}

	// Decode criteria token by token to keep the file's order; a map would
	// shuffle metrics in the results.
	dec := json.NewDecoder(bytes.NewReader(top.Criteria))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("criteria must be an object")
	}
	cfg := &EvalConfig{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("criterion %s: %w", name, err)
		}
		c, err := parseCriterion(name, raw)
		if err != nil {
			return nil, err
		}
		cfg.Criteria = append(cfg.Criteria, c)
	}
	if len(cfg.Criteria) == 0 {
		return nil, errors.New("criteria is empty")
	}
	return cfg, nil
}

func parseCriterion(name string, raw json.RawMessage) (Criterion, error) {
	if _, ok := metrics[name]; !ok {
		return Criterion{}, fmt.Errorf("metric %q is not supported; supported metrics: %s",
			name, strings.Join(SupportedMetrics(), ", "))
	}
	// A bare number is the threshold, as in adk-python.
	var threshold float64
	if err := json.Unmarshal(raw, &threshold); err == nil {
		return Criterion{
			MetricName: name,
			Threshold:  threshold,
			Raw:        json.RawMessage(fmt.Sprintf(`{"threshold":%v}`, threshold)),
		}, nil
	}
	var obj struct {
		Threshold *float64 `json:"threshold"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Criterion{}, fmt.Errorf("criterion %s: want a number or an object: %w", name, err)
	}
	if obj.Threshold == nil {
		return Criterion{}, fmt.Errorf("criterion %s: threshold is required", name)
	}
	return Criterion{MetricName: name, Threshold: *obj.Threshold, Raw: raw}, nil
}

// SupportedMetrics returns the metric names this package can run, sorted.
func SupportedMetrics() []string {
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
