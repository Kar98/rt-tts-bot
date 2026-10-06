package evals

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.36.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/telemetry"

	"github.com/Kar98/artosis-tts-agent/internal/observability"
)

// Suite runs every case in Set through Agent as one Langfuse experiment.
type Suite struct {
	Set   *EvalSet
	Agent agent.Agent
	// Metadata is stored on the experiment, e.g. the agent model.
	Metadata map[string]string
}

// Run runs each case as a subtest. A case fails only if the agent errors;
// scores come later from the Langfuse evaluators.
func (s *Suite) Run(t *testing.T) {
	ctx := t.Context()
	cfg, ok := observability.LoadLangfuseConfig(ctx, "")
	if !ok {
		t.Fatal("evals need Langfuse: set LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY")
	}
	setupTracing(t, cfg)
	lf := observability.NewLangfuseClient(cfg)

	datasetID, err := lf.UpsertDataset(ctx, s.Set.ID, s.Set.Description)
	if err != nil {
		t.Fatal(err)
	}
	name := experimentName(s.Set.ID)
	expAttrs := []attribute.KeyValue{
		attribute.String("langfuse.experiment.id", randomID()),
		attribute.String("langfuse.experiment.name", name),
		attribute.String("langfuse.experiment.dataset.id", datasetID),
	}
	for k, v := range s.Metadata {
		expAttrs = append(expAttrs, attribute.String("langfuse.experiment.metadata."+k, v))
	}
	t.Logf("Langfuse experiment %q on dataset %q", name, s.Set.ID)

	for _, c := range s.Set.Cases {
		t.Run(c.ID, func(t *testing.T) {
			ctx := t.Context()
			itemID := s.Set.ID + "/" + c.ID
			input := map[string]any{"input": c.Input, "session_state": c.State, "context": c.Context}
			var expected any
			if c.Reference != "" {
				expected = c.Reference
			}
			if err := lf.UpsertDatasetItem(ctx, s.Set.ID, itemID, input, expected); err != nil {
				t.Fatal(err)
			}

			// Each item is its own trace. Langfuse finds the item's root span by
			// root_observation_id, and needs the item attributes on every span
			// in the trace, so they go in ctx for spanAttrsProcessor.
			ctx, span := otel.Tracer("evals").Start(ctx, itemID, trace.WithNewRoot())
			itemAttrs := slices.Concat(expAttrs, []attribute.KeyValue{
				attribute.String("langfuse.experiment.item.id", itemID),
				attribute.String("langfuse.experiment.item.root_observation_id", span.SpanContext().SpanID().String()),
			})
			span.SetAttributes(itemAttrs...)
			span.SetAttributes(attribute.String("langfuse.observation.input", mustJSON(input)))
			if expected != nil {
				span.SetAttributes(attribute.String("langfuse.experiment.item.expected_output", mustJSON(expected)))
			}
			ctx = context.WithValue(ctx, spanAttrsKey{}, itemAttrs)

			out, err := RunAgent(ctx, s.Agent, c)
			if err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.End()
				t.Fatalf("running agent: %v", err)
			}
			span.SetAttributes(attribute.String("langfuse.observation.output", out))
			span.End()
			t.Logf("response: %s", out)
		})
	}
}

// setupTracing sends spans to Langfuse until the test ends.
func setupTracing(t *testing.T, cfg observability.LangfuseConfig) {
	for k, v := range observability.OTelEnv(cfg) {
		t.Setenv(k, v)
	}
	res, err := resource.New(t.Context(), resource.WithAttributes(semconv.ServiceNameKey.String("artosis-tts-agent-evals")))
	if err != nil {
		t.Fatal(err)
	}
	providers, err := telemetry.New(t.Context(),
		telemetry.WithResource(res),
		// Runs before the exporter ADK adds, so the attributes are exported.
		telemetry.WithSpanProcessors(spanAttrsProcessor{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	providers.SetGlobalOtelProviders()
	t.Cleanup(func() {
		// t.Context is cancelled by the time Cleanup runs.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(ctx); err != nil {
			t.Errorf("flushing traces to Langfuse: %v", err)
		}
	})
}

type spanAttrsKey struct{}

// spanAttrsProcessor copies the attributes stored under spanAttrsKey in the
// parent context onto each new span.
type spanAttrsProcessor struct{}

func (spanAttrsProcessor) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	if attrs, ok := ctx.Value(spanAttrsKey{}).([]attribute.KeyValue); ok {
		s.SetAttributes(attrs...)
	}
}
func (spanAttrsProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (spanAttrsProcessor) Shutdown(context.Context) error   { return nil }
func (spanAttrsProcessor) ForceFlush(context.Context) error { return nil }

// experimentName is "<set> <branch>@<short sha> <UTC time>". GitHub Actions sets
// GITHUB_REF_NAME and GITHUB_SHA; locally it asks git.
func experimentName(setID string) string {
	branch := firstNonEmpty(os.Getenv("GITHUB_REF_NAME"), git("rev-parse", "--abbrev-ref", "HEAD"), "unknown")
	sha := firstNonEmpty(os.Getenv("GITHUB_SHA"), git("rev-parse", "HEAD"), "unknown")
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return setID + " " + branch + "@" + sha + " " + time.Now().UTC().Format(time.RFC3339)
}

func git(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
