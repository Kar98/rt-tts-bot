package adkeval

import (
	"context"
	"iter"
	"sync/atomic"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// fakeLLM answers every request with reply(prompt). It counts how many calls
// are in flight at once.
type fakeLLM struct {
	reply func(prompt string) (string, error)
	delay time.Duration

	inFlight, maxInFlight, calls atomic.Int32
}

func (f *fakeLLM) Name() string { return "fake" }

func (f *fakeLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		f.calls.Add(1)
		n := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			m := f.maxInFlight.Load()
			if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		var prompt string
		if len(req.Contents) > 0 {
			prompt = contentText(req.Contents[len(req.Contents)-1])
		}
		text, err := f.reply(prompt)
		if err != nil {
			yield(nil, err)
			return
		}
		yield(&model.LLMResponse{
			Content:      genai.NewContentFromText(text, genai.RoleModel),
			ModelVersion: "fake-1",
		}, nil)
	}
}

func fakeJudgeFactory(reply func(prompt string) (string, error)) func(context.Context, string) (model.LLM, error) {
	if reply == nil {
		reply = func(string) (string, error) { return "", nil }
	}
	j := &fakeLLM{reply: reply, delay: time.Millisecond}
	return func(context.Context, string) (model.LLM, error) { return j, nil }
}
