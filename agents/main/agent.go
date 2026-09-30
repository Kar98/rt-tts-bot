// Command main runs the Twitch chat summary agent, locally or on Agent Engine.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

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

	// MODEL_LOCATION lets the model live in a different region (e.g. "global")
	// from the agent.
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

	// channelHint := "The user must name a channel."
	// if defaultChannel != "" {
	// 	channelHint = fmt.Sprintf("If the user does not name a channel, use %q.", twitchchat.NormaliseChannel(defaultChannel))
	// }
	mainAgent, err := llmagent.New(llmagent.Config{
		Name:        agentName,
		Model:       model,
		Description: "Reads a Twitch channel's chat and summarises it.",
		Instruction: `You summarise Twitch chat for the user.

When asked to summarise chat:
1. Call ` + tools.ReadChatToolName + ` with the channel, and with max_seconds and max_messages if the user gave them.
2. If the result has an "error", tell the user plainly what went wrong and stop.
3. Otherwise call ` + agents.SummariserName + ` with the request "Summarise the chat".
4. Reply with the summary exactly as returned, with no extra text.

For anything else, briefly explain that you summarise Twitch chat.`,
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
