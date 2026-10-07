package adkevaltest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Kar98/artosis-tts-agent/adkeval"
)

func TestWriteJudgeSamples(t *testing.T) {
	dir := t.TempDir()
	err := WriteJudgeSamples(dir, []adkeval.JudgeSample{
		{EvalID: "c1", Metric: "m", Turn: 1, Sample: 2, Prompt: "the prompt", Reply: "Verdict: yes"},
		{EvalID: "c1", Metric: "m", Turn: 1, Sample: 3, Prompt: "the prompt", Err: errors.New("quota")},
	})
	require.NoError(t, err)

	b, err := os.ReadFile(filepath.Join(dir, "c1", "m", "turn1_sample2.txt"))
	require.NoError(t, err)
	assert.Equal(t, "eval_id: c1\nmetric: m\nturn: 1\nsample: 2\n\n===== PROMPT =====\nthe prompt\n\n===== REPLY =====\nVerdict: yes\n", string(b))

	b, err = os.ReadFile(filepath.Join(dir, "c1", "m", "turn1_sample3.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "error: quota\n")
}
