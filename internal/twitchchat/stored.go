package twitchchat

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Embedded so the files are found whatever directory the agent runs from.
//
//go:embed message_data
var messageData embed.FS

// StoredSource will serve previously captured chat messages.

type StoredSource struct{}

func (StoredSource) Fetch(_ context.Context, req Request) ([]Message, error) {
	messages := []Message{}
	file, err := fs.ReadFile(messageData, "message_data/"+req.Channel)
	if err != nil {
		return nil, fmt.Errorf("no stored messages named %q", req.Channel)
	}

	filedata := string(file)
	splits := strings.SplitSeq(filedata, "\n")
	for split := range splits {
		if len(split) > 0 && split[0] == ':' {
			text := split[1:]
			messages = append(messages, Message{User: "unknown", Time: time.Time{}, Text: text})
		}
	}
	return messages, nil
}
