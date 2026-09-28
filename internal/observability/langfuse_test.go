package observability

import (
	"context"
	"os"
	"testing"
)

func TestOTelEnv(t *testing.T) {
	got := OTelEnv(LangfuseConfig{Host: "https://lf.example.com/", PublicKey: "pk", SecretKey: "sk"})
	if want := "https://lf.example.com/api/public/otel/v1/traces"; got[envTracesEndpoint] != want {
		t.Errorf("endpoint = %q, want %q", got[envTracesEndpoint], want)
	}
	// base64("pk:sk") = "cGs6c2s="
	if want := "Authorization=Basic cGs6c2s="; got[envHeaders] != want {
		t.Errorf("headers = %q, want %q", got[envHeaders], want)
	}
	if _, ok := got["OTEL_EXPORTER_OTLP_ENDPOINT"]; ok {
		t.Errorf("generic OTLP endpoint must not be set")
	}
}

func TestConfigureLangfuseFromEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_AGENT_ENGINE_ID", "")
	t.Setenv("LANGFUSE_HOST", "https://lf.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")
	t.Setenv(envTracesEndpoint, "")
	t.Setenv(envHeaders, "Authorization=keep-me")

	if err := ConfigureLangfuse(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(envTracesEndpoint); got != "https://lf.example.com/api/public/otel/v1/traces" {
		t.Errorf("endpoint = %q", got)
	}
	if got := os.Getenv(envHeaders); got != "Authorization=keep-me" {
		t.Errorf("existing headers were overwritten: %q", got)
	}
}

func TestConfigureLangfuseMissing(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_AGENT_ENGINE_ID", "")
	t.Setenv("LANGFUSE_HOST", "")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")
	t.Setenv(envTracesEndpoint, "")

	if err := ConfigureLangfuse(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(envTracesEndpoint); got != "" {
		t.Errorf("endpoint should be unset, got %q", got)
	}
}
