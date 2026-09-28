package main

import (
	"strings"
	"testing"
)

func TestReadEventStreamFinalReply(t *testing.T) {
	stream := `{"author":"twitch_chat_agent","content":{"parts":[{"function_call":{"name":"read_twitch_chat"}}]}}
{"author":"twitch_chat_agent","content":{"parts":[{"text":"Chat is hy"}]},"partial":true}
data: {"author":"twitch_chat_agent","content":{"parts":[{"text":"Chat is hyped about the game."}]}}

{"author":"chat_summariser","content":{"parts":[{"text":"not me"}]}}
`
	events, err := readEventStream(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	got, err := finalReply(events, appName)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Chat is hyped about the game." {
		t.Errorf("reply = %q", got)
	}
}

func TestFinalReplyError(t *testing.T) {
	events, _ := readEventStream(strings.NewReader(`{"error":"boom"}`))
	if _, err := finalReply(events, appName); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestBuildMessage(t *testing.T) {
	got := buildMessage(summariseRequest{Channel: "artosis", Seconds: 20, Messages: 50})
	if want := "Summarise the Twitch chat for channel artosis over 20 seconds, up to 50 messages."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
