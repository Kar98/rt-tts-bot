// Package observability configures trace export to Langfuse.
package observability

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

const (
	envTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envHeaders        = "OTEL_EXPORTER_OTLP_HEADERS"
	envCaptureContent = "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"
)

// LangfuseConfig holds the Langfuse connection details.
type LangfuseConfig struct {
	Host      string
	PublicKey string
	SecretKey string
}

func (c LangfuseConfig) complete() bool {
	return c.Host != "" && c.PublicKey != "" && c.SecretKey != ""
}

// setting pairs a config field with its env var and Secret Manager secret.
type setting struct {
	env, secret string
	field       func(*LangfuseConfig) *string
}

var settings = []setting{
	{"LANGFUSE_HOST", "langfuse-host", func(c *LangfuseConfig) *string { return &c.Host }},
	{"LANGFUSE_PUBLIC_KEY", "langfuse-public-key", func(c *LangfuseConfig) *string { return &c.PublicKey }},
	{"LANGFUSE_SECRET_KEY", "langfuse-secret-key", func(c *LangfuseConfig) *string { return &c.SecretKey }},
}

// ConfigureLangfuse points the ADK OTLP trace exporter at Langfuse by setting
// the OTEL_* env vars. It must run before the launcher starts telemetry.
//
// Values come from the LANGFUSE_* env vars. On Agent Engine (when
// GOOGLE_CLOUD_AGENT_ENGINE_ID is set), missing values are read from Secret
// Manager in project. If Langfuse is not fully configured, it logs a warning
// and returns nil so the agent still runs.
func ConfigureLangfuse(ctx context.Context, project string) error {
	var cfg LangfuseConfig
	var missing []setting
	for _, s := range settings {
		*s.field(&cfg) = strings.TrimSpace(os.Getenv(s.env))
		if *s.field(&cfg) == "" {
			missing = append(missing, s)
		}
	}

	if len(missing) > 0 && os.Getenv("GOOGLE_CLOUD_AGENT_ENGINE_ID") != "" && project != "" {
		if err := readSecrets(ctx, project, &cfg, missing); err != nil {
			log.Printf("WARNING: reading Langfuse secrets: %v", err)
		}
	}

	if !cfg.complete() {
		log.Printf("WARNING: Langfuse is not configured (set LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY); traces will not be sent to Langfuse")
		return nil
	}

	for k, v := range OTelEnv(cfg) {
		if os.Getenv(k) != "" {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("setting %s: %w", k, err)
		}
	}
	log.Printf("Langfuse tracing enabled: %s", os.Getenv(envTracesEndpoint))
	return nil
}

// OTelEnv returns the OTLP exporter env vars for cfg. Only the traces endpoint
// is set: Langfuse does not accept OTLP logs, and the generic
// OTEL_EXPORTER_OTLP_ENDPOINT would enable the ADK log exporter too.
//
// ADK leaves prompts and responses off model and agent spans by default, which
// Langfuse shows as null input and output. SPAN_ONLY puts them on the spans;
// the EVENT modes would put them in log records, which we don't export.
func OTelEnv(cfg LangfuseConfig) map[string]string {
	auth := base64.StdEncoding.EncodeToString([]byte(cfg.PublicKey + ":" + cfg.SecretKey))
	return map[string]string{
		envTracesEndpoint: strings.TrimRight(cfg.Host, "/") + "/api/public/otel/v1/traces",
		// The exporter splits on the first '=' and URL-unescapes the value;
		// base64 contains no '%' or ',' so it passes through unchanged.
		envHeaders:        "Authorization=Basic " + auth,
		envCaptureContent: "SPAN_ONLY",
	}
}

func readSecrets(ctx context.Context, project string, cfg *LangfuseConfig, missing []setting) error {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	for _, s := range missing {
		resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", project, s.secret),
		})
		if err != nil {
			return fmt.Errorf("secret %s: %w", s.secret, err)
		}
		*s.field(cfg) = strings.TrimSpace(string(resp.GetPayload().GetData()))
	}
	return nil
}
