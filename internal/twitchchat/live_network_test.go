package twitchchat

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveSourceNetwork reads real chat. Run with TWITCH_TEST_CHANNEL=<channel>.
func TestLiveSourceNetwork(t *testing.T) {
	channel := os.Getenv("TWITCH_TEST_CHANNEL")
	if channel == "" {
		t.Skip("TWITCH_TEST_CHANNEL not set")
	}
	msgs, err := (&LiveSource{}).Fetch(context.Background(), Request{Channel: channel, MaxDuration: 15 * time.Second, MaxMessages: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("got %d messages:\n%s", len(msgs), FormatTranscript(msgs))
}
