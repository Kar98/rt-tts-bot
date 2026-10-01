// Command main runs the Twitch chat summary agent, locally or on Agent Engine.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"strings"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.36.0"
	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/agentengine"
	"google.golang.org/adk/v2/cmd/launcher/full"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/session/vertexai"
	"google.golang.org/adk/v2/telemetry"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/agenttool"

	"github.com/Kar98/artosis-tts-agent/internal/agents"
	"github.com/Kar98/artosis-tts-agent/internal/observability"
	"github.com/Kar98/artosis-tts-agent/internal/tools"
	"github.com/Kar98/artosis-tts-agent/internal/twitchchat"
)

const (
	agentName    = "twitch_chat_agent"
	defaultModel = "gemini-3.5-flash"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := firstNonEmpty(os.Getenv("GOOGLE_CLOUD_AGENT_ENGINE_LOCATION"), os.Getenv("GOOGLE_CLOUD_LOCATION"), "us-central1")
	agentEngineID := os.Getenv("GOOGLE_CLOUD_AGENT_ENGINE_ID")
	defaultChannel := os.Getenv("TWITCH_DEFAULT_CHANNEL")

	if err := observability.ConfigureLangfuse(ctx, project); err != nil {
		return fmt.Errorf("failed to configure Langfuse: %w", err)
	}

	model, err := gemini.NewModel(ctx, firstNonEmpty(os.Getenv("MODEL"), defaultModel), &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  project,
		Location: firstNonEmpty(os.Getenv("MODEL_LOCATION"), location),
	})
	if err != nil {
		return fmt.Errorf("failed to create model: %w", err)
	}

	readChat, err := tools.NewReadChatTool(map[string]twitchchat.Source{
		"live":   &twitchchat.LiveSource{},
		"stored": twitchchat.StoredSource{},
	}, defaultChannel)
	if err != nil {
		return fmt.Errorf("failed to create read chat tool: %w", err)
	}

	summariser, err := agents.NewSummariser(model)
	if err != nil {
		return fmt.Errorf("failed to create summariser: %w", err)
	}

	varMapping := map[string]string{
		"tools.ReadChatToolName": tools.ReadChatToolName,
		"agents.SummariserName":  agents.SummariserName,
	}
	instruction, err := loadInstruction(varMapping)
	if err != nil {
		return err
	}
	mainAgent, err := llmagent.New(llmagent.Config{
		Name:        agentName,
		Model:       model,
		Description: "Reads a Twitch channel's chat and summarises it.",
		Instruction: instruction,
		Tools: []tool.Tool{
			readChat,
			agenttool.New(summariser, nil),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceNameKey.String("artosis-tts-agent"),
	))
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}
	config := &launcher.Config{
		AgentLoader:      agent.NewSingleLoader(mainAgent),
		TelemetryOptions: []telemetry.Option{telemetry.WithResource(res)},
	}

	var l launcher.Launcher
	if agentEngineID != "" {
		config.SessionService, err = vertexai.NewSessionService(ctx, vertexai.VertexAIServiceConfig{
			ProjectID:       project,
			Location:        location,
			ReasoningEngine: agentEngineID,
		})
		if err != nil {
			return fmt.Errorf("failed to create session service: %w", err)
		}
		l = agentengine.NewLauncher(agentEngineID)
	} else {
		l = full.NewLauncher()
	}

	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		return fmt.Errorf("run failed: %w\n\n%s", err, l.CommandLineSyntax())
	}
	return nil
}

// Embedded so the agent finds it whatever directory it runs from.
//
//go:embed instruction.md
var instructionTemplate string

func loadInstruction(mappings map[string]string) (string, error) {
	tostr := instructionTemplate
	for k, v := range mappings {
		keytoreplace := "{" + k + "}"
		tostr = strings.ReplaceAll(tostr, keytoreplace, v)
	}
	return tostr, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
