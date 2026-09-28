// Package tools holds the function tools used by the Twitch chat agent.
package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/Kar98/artosis-tts-agent/internal/twitchchat"
)

// TranscriptStateKey is the session state key the formatted chat transcript is
// written to. The summariser reads it through its instruction template.
const TranscriptStateKey = "chat_transcript"

// ReadChatToolName is the name the model uses to call the tool.
const ReadChatToolName = "read_twitch_chat"

const (
	defaultSource      = "live"
	defaultMaxSeconds  = 30
	maxMaxSeconds      = 60
	defaultMaxMessages = 100
	maxMaxMessages     = 300
)

// ReadChatArgs are the arguments the model passes to read_twitch_chat.
type ReadChatArgs struct {
	Channel     string `json:"channel,omitempty" jsonschema:"Twitch channel name to read, without '#'. Omit to use the default channel."`
	MaxSeconds  int    `json:"max_seconds,omitempty" jsonschema:"Stop listening after this many seconds (1-60, default 30)."`
	MaxMessages int    `json:"max_messages,omitempty" jsonschema:"Stop after this many messages (1-300, default 100)."`
	Source      string `json:"source,omitempty" jsonschema:"Where to read messages from: 'live' (default) listens to chat now, 'stored' reads saved messages."`
}

// ReadChatResult is returned to the model. The transcript itself is not
// included; it is stored in session state under TranscriptStateKey.
type ReadChatResult struct {
	Channel         string  `json:"channel"`
	Source          string  `json:"source"`
	Count           int     `json:"count"`
	DurationSeconds float64 `json:"duration_seconds"`
	Error           string  `json:"error,omitempty"`
}

// NewReadChatTool builds the read_twitch_chat tool over the given sources,
// keyed by the name the model passes as "source".
func NewReadChatTool(sources map[string]twitchchat.Source, defaultChannel string) (tool.Tool, error) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	return functiontool.New(functiontool.Config{
		Name: ReadChatToolName,
		Description: "Reads messages from a Twitch channel's chat and saves them for the chat_summariser tool. " +
			"Returns only how many messages were collected, not the messages themselves. " +
			"Available sources: " + strings.Join(names, ", ") + ".",
	}, func(ctx agent.Context, args ReadChatArgs) (ReadChatResult, error) {
		return readChat(ctx, ctx.State(), sources, defaultChannel, args), nil
	})
}

// readChat does the work of the tool. Failures are reported in the result so
// the agent can relay them to the user.
func readChat(ctx context.Context, state session.State, sources map[string]twitchchat.Source, defaultChannel string, args ReadChatArgs) ReadChatResult {
	channel := twitchchat.NormaliseChannel(args.Channel)
	if channel == "" {
		channel = twitchchat.NormaliseChannel(defaultChannel)
	}
	sourceName := strings.ToLower(strings.TrimSpace(args.Source))
	if sourceName == "" {
		sourceName = defaultSource
	}
	res := ReadChatResult{Channel: channel, Source: sourceName}

	if channel == "" {
		res.Error = "no channel given and no default channel is configured"
		return res
	}
	src, ok := sources[sourceName]
	if !ok {
		res.Error = fmt.Sprintf("unknown source %q", sourceName)
		return res
	}

	req := twitchchat.Request{
		Channel:     channel,
		MaxDuration: time.Duration(clamp(args.MaxSeconds, defaultMaxSeconds, 1, maxMaxSeconds)) * time.Second,
		MaxMessages: clamp(args.MaxMessages, defaultMaxMessages, 1, maxMaxMessages),
	}
	start := time.Now()
	msgs, err := src.Fetch(ctx, req)
	res.DurationSeconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
	if errors.Is(err, twitchchat.ErrNotImplemented) {
		res.Error = fmt.Sprintf("the %q source is not implemented yet", sourceName)
		return res
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}

	if err := state.Set(TranscriptStateKey, twitchchat.FormatTranscript(msgs)); err != nil {
		res.Error = fmt.Sprintf("saving transcript: %v", err)
		return res
	}
	res.Count = len(msgs)
	return res
}

// clamp returns def when v is unset (<= 0), otherwise v limited to [lo, hi].
func clamp(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	return min(max(v, lo), hi)
}
