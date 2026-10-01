// Package twitchchat fetches messages from Twitch channel chats.
package twitchchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message is a single chat message.
type Message struct {
	User string
	Text string
	Time time.Time
}

// Request describes which messages to fetch.
type Request struct {
	// Channel is the Twitch channel name, or message data to load
	Channel string
	// MaxDuration bounds how long a live source listens for.
	MaxDuration time.Duration
	// MaxMessages bounds how many messages are returned.
	MaxMessages int
}

// Source returns chat messages for a channel.
type Source interface {
	Fetch(ctx context.Context, req Request) ([]Message, error)
}

// ErrNotImplemented is returned by sources that are not available yet.
var ErrNotImplemented = errors.New("twitchchat: source not implemented")

// FormatTranscript renders messages one per line as "[15:04:05] user: text".
func FormatTranscript(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.Time.UTC().Format("15:04:05"), m.User, m.Text)
	}
	return b.String()
}

// NormaliseChannel lower-cases a channel name and strips a leading '#'.
func NormaliseChannel(channel string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(channel), "#"))
}
