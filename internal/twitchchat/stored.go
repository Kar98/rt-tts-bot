package twitchchat

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// Embedded so the files are found whatever directory the agent runs from.
//
//go:embed message_data
var messageData embed.FS

// StoredSource will serve previously captured chat messages.

type TwitchChatFormat string

const (
	Safari  TwitchChatFormat = "safari"
	Firefox TwitchChatFormat = "firefox"
)

const safariRegexp = `^\d:\d\d:\d\d`

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

	// get format
	filedata := string(file)
	splits := strings.Split(filedata, "\n")
	if len(splits) == 0 {
		slog.Warn("no messages were found", "req.Channel", req.Channel)
		return messages, nil
	}
	chatFormat := getFormat(splits[0])
	if chatFormat == Safari {
		for i := 0; i < len(splits); i++ {
			line := splits[i]
			match, _ := regexp.MatchString(safariRegexp, line)
			if match {
				i++
				line = splits[i]
				idx := strings.Index(line, ":") + 1 // exclude the :
				chat := line[idx:]
				messages = append(messages, Message{User: "unknown", Time: time.Time{}, Text: chat})
			}
		}
	}
	if chatFormat == Firefox {
		for _, split := range splits {
			text := split[2:] // whitespace char as well ': '
			messages = append(messages, Message{User: "unknown", Time: time.Time{}, Text: text})
		}
	}

	if len(messages) == 0 {
		slog.Error("no messages after parsing chat logs", "req.Channel", req.Channel, "format", chatFormat)
	}

	return messages, nil
}

func getFormat(firstLine string) TwitchChatFormat {
	slog.Info("hi")
	match, err := regexp.MatchString(safariRegexp, firstLine)
	if err != nil {
		slog.Error("could not get stored twitch chat format", "err", err.Error())
	}
	if match {
		return Safari
	}
	return Firefox
}
