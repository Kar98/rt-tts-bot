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

func TestBuildMessageStored(t *testing.T) {
	got := buildMessage(summariseRequest{Channel: "artosis.txt", Seconds: 20, Messages: 50, Stored: true})
	if want := "Summarise the Twitch chat from the stored messages (source: stored) in artosis.txt, up to 50 messages."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLastSummary(t *testing.T) {
	for name, stream := range map[string]string{
		"adk rest": `{"author":"twitch_chat_agent","content":{"parts":[{"functionResponse":{"name":"chat_summariser","response":{"result":"old"}}}]}}
{"author":"twitch_chat_agent","content":{"parts":[{"functionResponse":{"name":"read_twitch_chat","response":{"count":3}}}]}}
{"author":"twitch_chat_agent","content":{"parts":[{"functionResponse":{"name":"chat_summariser","response":{"result":" Chat loves the build. "}}}]}}
{"author":"twitch_chat_agent","content":{"parts":[{"text":"Chat loves the build."}]}}`,
		"agent engine": `{"author":"twitch_chat_agent","content":{"parts":[{"function_response":{"name":"chat_summariser","response":{"result":"Chat loves the build."}}}]}}`,
	} {
		events, err := readEventStream(strings.NewReader(stream))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := lastSummary(events); got != "Chat loves the build." {
			t.Errorf("%s: summary = %q", name, got)
		}
	}
}

func TestLastSummaryNone(t *testing.T) {
	events, _ := readEventStream(strings.NewReader(`{"author":"twitch_chat_agent","content":{"parts":[{"text":"hi"}]}}`))
	if got := lastSummary(events); got != "" {
		t.Errorf("summary = %q, want empty", got)
	}
}
