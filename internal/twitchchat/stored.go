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
	filepath := "message_data/" + req.Channel
	file, err := fs.ReadFile(messageData, filepath)
	if err != nil {
		// Try with a "txt" as well
		filepath = filepath + ".txt"
		file, err = fs.ReadFile(messageData, filepath)
		if err != nil {
			return nil, fmt.Errorf("no stored messages named %q", req.Channel)
		}
	}

	filedata := string(file)
	splits := strings.SplitSeq(filedata, "\n")
	for split := range splits {
		if len(split) > 0 && split[0] == ':' {
			text := split[2:] // whitespace char as well ': '
			messages = append(messages, Message{User: "unknown", Time: time.Time{}, Text: text})
		}
	}
	return messages, nil
}
