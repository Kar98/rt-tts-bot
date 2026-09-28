package twitchchat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v4"
)

// fakeClient emits the configured messages once connected and blocks in
// Connect until Disconnect is called.
type fakeClient struct {
	msgs       []string
	interval   time.Duration
	connectErr error

	mu      sync.Mutex
	handler func(twitch.PrivateMessage)
	joined  []string
	done    chan struct{}
	once    sync.Once
}

func newFake(msgs ...string) *fakeClient {
	return &fakeClient{msgs: msgs, done: make(chan struct{})}
}

func (f *fakeClient) OnPrivateMessage(h func(twitch.PrivateMessage)) { f.handler = h }
func (f *fakeClient) Join(channels ...string)                        { f.joined = append(f.joined, channels...) }

func (f *fakeClient) Connect() error {
	if f.connectErr != nil {
		return f.connectErr
	}
	for i, text := range f.msgs {
		if f.interval > 0 {
			select {
			case <-time.After(f.interval):
			case <-f.done:
				return twitch.ErrClientDisconnected
			}
		}
		f.handler(twitch.PrivateMessage{
			User:    twitch.User{Name: fmt.Sprintf("user%d", i), DisplayName: fmt.Sprintf("User%d", i)},
			Message: text,
			Time:    time.Date(2026, 9, 28, 12, 0, i, 0, time.UTC),
		})
	}
	<-f.done
	return twitch.ErrClientDisconnected
}

func (f *fakeClient) Disconnect() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

func sourceWith(c *fakeClient) *LiveSource {
	return &LiveSource{newClient: func() ircClient { return c }}
}

func TestLiveSourceStopsAtMaxMessages(t *testing.T) {
	c := newFake("a", "b", "c", "d", "e")
	start := time.Now()
	got, err := sourceWith(c).Fetch(context.Background(), Request{Channel: "#Artosis", MaxDuration: 10 * time.Second, MaxMessages: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("Fetch did not stop early at max messages")
	}
	if got[0].User != "User0" || got[0].Text != "a" {
		t.Errorf("first message = %+v", got[0])
	}
	if len(c.joined) != 1 || c.joined[0] != "artosis" {
		t.Errorf("joined %v, want [artosis]", c.joined)
	}
}

func TestLiveSourceStopsAtTimeout(t *testing.T) {
	c := newFake("a", "b", "c", "d", "e")
	c.interval = 40 * time.Millisecond
	got, err := sourceWith(c).Fetch(context.Background(), Request{Channel: "x", MaxDuration: 100 * time.Millisecond, MaxMessages: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || len(got) >= 5 {
		t.Errorf("got %d messages, want between 1 and 4", len(got))
	}
}

func TestLiveSourceEmpty(t *testing.T) {
	got, err := sourceWith(newFake()).Fetch(context.Background(), Request{Channel: "x", MaxDuration: 50 * time.Millisecond, MaxMessages: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

func TestLiveSourceContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := sourceWith(newFake()).Fetch(ctx, Request{Channel: "x", MaxDuration: 10 * time.Second, MaxMessages: 10})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestLiveSourceConnectError(t *testing.T) {
	c := newFake()
	c.connectErr = errors.New("boom")
	_, err := sourceWith(c).Fetch(context.Background(), Request{Channel: "x", MaxDuration: 10 * time.Second, MaxMessages: 10})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLiveSourceValidates(t *testing.T) {
	s := sourceWith(newFake())
	for _, req := range []Request{
		{Channel: "", MaxDuration: time.Second, MaxMessages: 1},
		{Channel: "x", MaxDuration: 0, MaxMessages: 1},
		{Channel: "x", MaxDuration: time.Second, MaxMessages: 0},
	} {
		if _, err := s.Fetch(context.Background(), req); err == nil {
			t.Errorf("Fetch(%+v) want error", req)
		}
	}
}

func TestStoredSourceNotImplemented(t *testing.T) {
	_, err := StoredSource{}.Fetch(context.Background(), Request{})
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("err = %v, want ErrNotImplemented", err)
	}
}

func TestFormatTranscript(t *testing.T) {
	msgs := []Message{
		{User: "alice", Text: "hi", Time: time.Date(2026, 1, 1, 15, 4, 5, 0, time.UTC)},
		{User: "bob", Text: "gg", Time: time.Date(2026, 1, 1, 15, 4, 6, 0, time.UTC)},
	}
	want := "[15:04:05] alice: hi\n[15:04:06] bob: gg\n"
	if got := FormatTranscript(msgs); got != want {
		t.Errorf("FormatTranscript = %q, want %q", got, want)
	}
	if got := FormatTranscript(nil); got != "" {
		t.Errorf("FormatTranscript(nil) = %q, want empty", got)
	}
}
