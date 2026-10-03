package tools

import (
	"context"
	"iter"
	"maps"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/Kar98/artosis-tts-agent/internal/twitchchat"
)

type mapState map[string]any

func (m mapState) Get(k string) (any, error) {
	v, ok := m[k]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}
func (m mapState) Set(k string, v any) error   { m[k] = v; return nil }
func (m mapState) All() iter.Seq2[string, any] { return maps.All(m) }

type fakeSource struct {
	got  twitchchat.Request
	msgs []twitchchat.Message
}

func (f *fakeSource) Fetch(_ context.Context, req twitchchat.Request) ([]twitchchat.Message, error) {
	f.got = req
	return f.msgs, nil
}

func TestReadChatDefaults(t *testing.T) {
	src := &fakeSource{msgs: []twitchchat.Message{{User: "a", Text: "hi", Time: time.Unix(0, 0)}}}
	state := mapState{}
	res := readChat(context.Background(), state, map[string]twitchchat.Source{"live": src}, "#Artosis", ReadChatArgs{})

	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if src.got.Channel != "artosis" || src.got.MaxDuration != 30*time.Second || src.got.MaxMessages != 100 {
		t.Errorf("request = %+v", src.got)
	}
	if res.Channel != "artosis" || res.Source != "live" || res.Count != 1 {
		t.Errorf("result = %+v", res)
	}
	if got, _ := state[TranscriptStateKey].(string); !strings.Contains(got, "a: hi") {
		t.Errorf("transcript = %q", got)
	}
	if got, _ := state[MessagesStateKey].([]string); len(got) != 1 || got[0] != "hi" {
		t.Errorf("messages = %q", got)
	}
}

func TestReadChatClamps(t *testing.T) {
	for _, tc := range []struct {
		secs, msgs         int
		wantSecs, wantMsgs int
	}{
		{secs: 1000, msgs: 1000, wantSecs: 60, wantMsgs: 300},
		{secs: 5, msgs: 7, wantSecs: 5, wantMsgs: 7},
		{secs: -3, msgs: -1, wantSecs: 30, wantMsgs: 100},
	} {
		src := &fakeSource{}
		readChat(context.Background(), mapState{}, map[string]twitchchat.Source{"live": src}, "", ReadChatArgs{Channel: "x", MaxSeconds: tc.secs, MaxMessages: tc.msgs})
		if src.got.MaxDuration != time.Duration(tc.wantSecs)*time.Second || src.got.MaxMessages != tc.wantMsgs {
			t.Errorf("secs=%d msgs=%d: request = %+v", tc.secs, tc.msgs, src.got)
		}
	}
}

func TestReadChatErrors(t *testing.T) {
	sources := map[string]twitchchat.Source{"live": &fakeSource{}, "stored": twitchchat.StoredSource{}}
	for name, args := range map[string]ReadChatArgs{
		"not implemented": {Channel: "x", Source: "stored"},
		"unknown source":  {Channel: "x", Source: "nope"},
		"no channel":      {},
	} {
		state := mapState{}
		res := readChat(context.Background(), state, sources, "", args)
		if res.Error == "" {
			t.Errorf("%s: want error", name)
		}
		if _, ok := state[TranscriptStateKey]; ok {
			t.Errorf("%s: transcript should not be set", name)
		}
	}
}

func TestNewReadChatTool(t *testing.T) {
	tl, err := NewReadChatTool(map[string]twitchchat.Source{"live": &fakeSource{}}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if tl.Name() != ReadChatToolName {
		t.Errorf("name = %q", tl.Name())
	}
}
