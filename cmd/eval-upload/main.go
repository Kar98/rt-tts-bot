// Command eval-upload sends an adkeval result file to Langfuse as a dataset
// experiment, with the scores the eval already computed. Langfuse does no
// scoring.
//
// Each eval case becomes a dataset item and one trace in the experiment, and
// each metric and rubric verdict becomes a score on that trace. Trace and
// score ids are derived from the result id, so uploading the same file again
// overwrites rather than duplicates.
//
// Traces go through Langfuse's OpenTelemetry endpoint with its
// langfuse.experiment.* attributes; Langfuse 4 deprecates the older ingestion
// and dataset-run-items endpoints.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.36.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/Kar98/artosis-tts-agent/adkeval"
	"github.com/Kar98/artosis-tts-agent/internal/observability"
)

func main() {
	file := flag.String("file", "", "result file to upload")
	dir := flag.String("dir", "internal/agents/dono_generator/.adk/eval_history", "upload the newest result file in this directory, if -file is not set")
	flag.Parse()

	if err := run(context.Background(), *file, *dir); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, file, dir string) error {
	if file == "" {
		var err error
		if file, err = newestResult(dir); err != nil {
			return err
		}
	}
	res, err := adkeval.LoadResult(file)
	if err != nil {
		return err
	}
	cfg, ok := observability.LoadLangfuseConfig(ctx, "")
	if !ok {
		return errors.New("set LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY")
	}
	lf := observability.NewLangfuseClient(cfg)
	log.Printf("uploading %s", file)

	datasetID, err := lf.UpsertDataset(ctx, res.EvalSetID, "")
	if err != nil {
		return err
	}

	tp, err := newTracerProvider(ctx, cfg)
	if err != nil {
		return err
	}
	tracer := tp.Tracer("eval-upload")
	expAttrs := []attribute.KeyValue{
		attribute.String("langfuse.experiment.id", hashHex(res.EvalSetResultID)[:32]),
		attribute.String("langfuse.experiment.name", res.EvalSetResultName),
		attribute.String("langfuse.experiment.dataset.id", datasetID),
		attribute.String("langfuse.experiment.metadata.eval_set_result_id", res.EvalSetResultID),
	}

	var scores []observability.Score
	for _, c := range res.EvalCaseResults {
		itemID := res.EvalSetID + "/" + c.EvalID
		input := map[string]any{"eval_id": c.EvalID, "user_content": turnTexts(c, expectedUser)}
		var expected any
		if texts := turnTexts(c, expectedFinal); slices.ContainsFunc(texts, func(s string) bool { return s != "" }) {
			expected = texts
		}
		if err := lf.UpsertDatasetItem(ctx, res.EvalSetID, itemID, input, expected); err != nil {
			return fmt.Errorf("dataset item %s: %w", itemID, err)
		}

		sum := sha256.Sum256([]byte(res.EvalSetResultID + "/" + c.EvalID))
		var traceID trace.TraceID
		var spanID trace.SpanID
		copy(traceID[:], sum[:16])
		copy(spanID[:], sum[16:24])

		start, end := caseTimes(res, c)
		spanCtx := withIDs(ctx, traceID, spanID)
		_, span := tracer.Start(spanCtx, c.EvalID, trace.WithNewRoot(), trace.WithTimestamp(start))
		span.SetAttributes(expAttrs...)
		span.SetAttributes(
			attribute.String("langfuse.experiment.item.id", itemID),
			attribute.String("langfuse.experiment.item.root_observation_id", spanID.String()),
			attribute.String("langfuse.observation.input", mustJSON(input)),
			attribute.String("langfuse.observation.output", strings.Join(turnTexts(c, actualFinal), "\n")),
			attribute.String("langfuse.observation.metadata.final_eval_status", c.FinalEvalStatus.String()),
		)
		if expected != nil {
			span.SetAttributes(attribute.String("langfuse.experiment.item.expected_output", mustJSON(expected)))
		}
		if c.SessionDetails != nil {
			span.SetAttributes(attribute.String("langfuse.observation.metadata.session_state", mustJSON(c.SessionDetails.State)))
		}
		if v := modelVersion(c); v != "" {
			span.SetAttributes(attribute.String("langfuse.observation.metadata.model_version", v))
		}
		span.End(trace.WithTimestamp(end))

		scores = append(scores, caseScores(traceID.String(), c)...)
	}

	// Send the traces before their scores.
	if err := tp.Shutdown(ctx); err != nil {
		return fmt.Errorf("sending traces: %w", err)
	}
	for _, s := range scores {
		if err := lf.CreateScore(ctx, s); err != nil {
			return fmt.Errorf("score %s: %w", s.Name, err)
		}
	}
	log.Printf("uploaded experiment %q: %d cases, %d scores, dataset %s",
		res.EvalSetResultName, len(res.EvalCaseResults), len(scores), res.EvalSetID)
	return nil
}

// caseScores returns a score for each metric with a score, and one for each
// rubric verdict, named <metric>/<rubric_id> with the judge's rationale.
func caseScores(traceID string, c adkeval.EvalCaseResult) []observability.Score {
	var out []observability.Score
	add := func(name string, value float64, comment string) {
		out = append(out, observability.Score{
			ID:      hashHex(traceID + "/" + name)[:32],
			TraceID: traceID,
			Name:    name,
			Value:   value,
			Comment: comment,
		})
	}
	for _, m := range c.OverallEvalMetricResults {
		if m.Score == nil {
			continue
		}
		comment := m.EvalStatus.String()
		if m.Threshold != nil {
			comment += fmt.Sprintf(", threshold %g", *m.Threshold)
		}
		add(m.MetricName, *m.Score, comment)
	}
	multiTurn := len(c.EvalMetricResultPerInvocation) > 1
	for i, inv := range c.EvalMetricResultPerInvocation {
		for _, m := range inv.EvalMetricResults {
			for _, r := range m.Details.RubricScores {
				if r.Score == nil {
					continue
				}
				name := m.MetricName + "/" + r.RubricID
				if multiTurn {
					name += fmt.Sprintf("/turn%d", i+1)
				}
				rationale := ""
				if r.Rationale != nil {
					rationale = *r.Rationale
				}
				add(name, *r.Score, rationale)
			}
		}
	}
	return out
}

type turnText func(adkeval.EvalMetricResultPerInvocation) *adkeval.Content

func expectedUser(p adkeval.EvalMetricResultPerInvocation) *adkeval.Content {
	if p.ExpectedInvocation == nil {
		return p.ActualInvocation.UserContent
	}
	return p.ExpectedInvocation.UserContent
}

func expectedFinal(p adkeval.EvalMetricResultPerInvocation) *adkeval.Content {
	if p.ExpectedInvocation == nil {
		return nil
	}
	return p.ExpectedInvocation.FinalResponse
}

func actualFinal(p adkeval.EvalMetricResultPerInvocation) *adkeval.Content {
	return p.ActualInvocation.FinalResponse
}

// turnTexts returns the text that pick selects from each turn of c.
func turnTexts(c adkeval.EvalCaseResult, pick turnText) []string {
	texts := make([]string, 0, len(c.EvalMetricResultPerInvocation))
	for _, p := range c.EvalMetricResultPerInvocation {
		var parts []string
		if content := pick(p); content != nil {
			for _, part := range content.GenAI().Parts {
				if part.Text != "" && !part.Thought {
					parts = append(parts, part.Text)
				}
			}
		}
		texts = append(texts, strings.Join(parts, "\n"))
	}
	return texts
}

// caseTimes returns when the case's first turn started and its last turn
// ended, falling back to the result's creation time.
func caseTimes(res *adkeval.EvalSetResult, c adkeval.EvalCaseResult) (start, end time.Time) {
	start = fromUnix(res.CreationTimestamp)
	end = start
	for i, p := range c.EvalMetricResultPerInvocation {
		inv := p.ActualInvocation
		if inv.CreationTimestamp == 0 {
			continue
		}
		t := fromUnix(inv.CreationTimestamp)
		if i == 0 {
			start = t
		}
		end = t
		if inv.Duration != nil {
			end = t.Add(time.Duration(*inv.Duration * float64(time.Second)))
		}
	}
	return start, end
}

func modelVersion(c adkeval.EvalCaseResult) string {
	for _, p := range c.EvalMetricResultPerInvocation {
		if d := p.ActualInvocation.IntermediateData; d != nil {
			for _, ev := range d.InvocationEvents {
				if ev.ModelVersion != "" {
					return ev.ModelVersion
				}
			}
		}
	}
	return ""
}

func newestResult(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.evalset_result.json"))
	if err != nil {
		return "", err
	}
	var newest string
	var newestTime time.Time
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return "", err
		}
		if info.ModTime().After(newestTime) {
			newest, newestTime = m, info.ModTime()
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no *.evalset_result.json in %s; run `just eval-dono` first", dir)
	}
	return newest, nil
}

func newTracerProvider(ctx context.Context, cfg observability.LangfuseConfig) (*sdktrace.TracerProvider, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(strings.TrimRight(cfg.Host, "/")+"/api/public/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{"Authorization": "Basic " + auth}),
	)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceNameKey.String("adkeval-upload")))
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithIDGenerator(fixedIDs{}),
	), nil
}

type idsKey struct{}

type spanIDs struct {
	trace trace.TraceID
	span  trace.SpanID
}

func withIDs(ctx context.Context, t trace.TraceID, s trace.SpanID) context.Context {
	return context.WithValue(ctx, idsKey{}, spanIDs{t, s})
}

// fixedIDs gives each span the ids stored in its context by withIDs, so a
// re-upload reuses the same trace ids.
type fixedIDs struct{}

func (fixedIDs) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	ids, ok := ctx.Value(idsKey{}).(spanIDs)
	if !ok {
		panic("eval-upload: span started without withIDs")
	}
	return ids.trace, ids.span
}

func (fixedIDs) NewSpanID(ctx context.Context, _ trace.TraceID) trace.SpanID {
	ids, ok := ctx.Value(idsKey{}).(spanIDs)
	if !ok {
		panic("eval-upload: span started without withIDs")
	}
	return ids.span
}

func fromUnix(secs float64) time.Time {
	return time.UnixMicro(int64(secs * 1e6))
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
