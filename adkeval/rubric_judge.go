package adkeval

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// This file is the LLM judge shared by the rubric_based_* metrics. It follows
// adk-python's rubric_based_evaluator.py: each turn's rubrics go into a
// prompt, the judge answers yes or no per rubric, several samples are
// combined by majority vote, and scores are means of the rubric verdicts.

const defaultJudgeModel = "gemini-2.5-flash"

const aggregatedRationale = "This is an aggregated score derived from individual entries." +
	" Please refer to individual entries in each invocation for actual rationale from the model."

// promptHeaderEnd ends the provenance header of the files in prompts/.
const promptHeaderEnd = "-----\n"

func loadPrompt(file string) string {
	_, body, ok := strings.Cut(file, promptHeaderEnd)
	if !ok {
		panic("adkeval: prompt file has no header line")
	}
	return body
}

// fillPrompt replaces each {name} in tmpl with vals[name].
func fillPrompt(tmpl string, vals map[string]string) string {
	pairs := make([]string, 0, 2*len(vals))
	for k, v := range vals {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

type judgeOptions struct {
	JudgeModel       string `json:"judge_model"`
	NumSamples       int    `json:"num_samples"`
	ParallelismLimit int    `json:"parallelism_limit"`
}

// rubricJudge holds what the three rubric metrics share. Each metric supplies
// its rubric type and how to build the prompt.
type rubricJudge struct {
	metric     string
	rubricType string
	threshold  float64
	rubrics    []Rubric
	opts       judgeOptions
	llm        model.LLM
}

func newRubricJudge(ctx context.Context, c Criterion, judges *judgeModels, rubricType string) (*rubricJudge, error) {
	var crit struct {
		Rubrics           []Rubric      `json:"rubrics"`
		JudgeModelOptions *judgeOptions `json:"judge_model_options"`
	}
	if err := json.Unmarshal(c.Raw, &crit); err != nil {
		return nil, fmt.Errorf("%s: %w", c.MetricName, err)
	}
	opts := judgeOptions{JudgeModel: defaultJudgeModel, NumSamples: 5, ParallelismLimit: 1}
	if o := crit.JudgeModelOptions; o != nil {
		if o.JudgeModel != "" {
			opts.JudgeModel = o.JudgeModel
		}
		if o.NumSamples != 0 {
			opts.NumSamples = o.NumSamples
		}
		if o.ParallelismLimit != 0 {
			opts.ParallelismLimit = o.ParallelismLimit
		}
	}
	if opts.NumSamples < 1 || opts.ParallelismLimit < 1 {
		return nil, fmt.Errorf("%s: num_samples and parallelism_limit must be at least 1", c.MetricName)
	}
	llm, err := judges.get(ctx, opts.JudgeModel)
	if err != nil {
		return nil, fmt.Errorf("%s: judge model %s: %w", c.MetricName, opts.JudgeModel, err)
	}
	return &rubricJudge{
		metric:     c.MetricName,
		rubricType: rubricType,
		threshold:  c.Threshold,
		rubrics:    crit.Rubrics,
		opts:       opts,
		llm:        llm,
	}, nil
}

// effectiveRubrics returns the criterion's rubrics plus inv's rubrics of this
// metric's type. inv's rubrics already include the case's and the expected
// turn's (see copyRubrics).
func (j *rubricJudge) effectiveRubrics(inv Invocation) ([]Rubric, error) {
	var out []Rubric
	seen := map[string]bool{}
	add := func(r Rubric, scope string) error {
		if seen[r.RubricID] {
			return fmt.Errorf("rubric_id %q in %s conflicts with an existing rubric", r.RubricID, scope)
		}
		seen[r.RubricID] = true
		out = append(out, r)
		return nil
	}
	for _, r := range j.rubrics {
		if err := add(r, "criterion"); err != nil {
			return nil, err
		}
	}
	for _, r := range inv.Rubrics {
		if r.Type != j.rubricType {
			continue
		}
		if err := add(r, "invocation"); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, errors.New("rubrics are required")
	}
	return out, nil
}

// judgeTask is one turn to judge: its prompt and rubrics.
type judgeTask struct {
	// turn is the 1-based turn the task judges.
	turn    int
	prompt  string
	rubrics []Rubric
}

// judge runs every task's samples, at most parallelism_limit at once across
// all tasks, and returns one result per task. If any sample of a task fails,
// that task is NOT_EVALUATED.
func (j *rubricJudge) judge(ctx context.Context, tasks []judgeTask) []InvocationResult {
	samples := make([][]string, len(tasks))
	errs := make([]error, len(tasks))
	for i := range tasks {
		samples[i] = make([]string, j.opts.NumSamples)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, j.opts.ParallelismLimit)
	for i, task := range tasks {
		for s := range j.opts.NumSamples {
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				text, err := j.sample(ctx, task.prompt)
				reportSample(ctx, JudgeSample{
					Metric: j.metric,
					Turn:   task.turn,
					Sample: s + 1,
					Prompt: task.prompt,
					Reply:  text,
					Err:    err,
				})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs[i] = errors.Join(errs[i], err)
					return
				}
				samples[i][s] = text
			})
		}
	}
	wg.Wait()

	results := make([]InvocationResult, len(tasks))
	for i, task := range tasks {
		if errs[i] != nil {
			results[i] = InvocationResult{Status: StatusNotEvaluated}
			continue
		}
		var perSample [][]RubricScore
		for _, text := range samples[i] {
			perSample = append(perSample, matchRubrics(parseVerdicts(text), task.rubrics))
		}
		scores := majorityVote(perSample)
		score := mean(rubricScoreValues(scores))
		results[i] = InvocationResult{Score: score, Status: statusFor(score, j.threshold), RubricScores: scores}
	}
	return results
}

// sample sends prompt to the judge once and returns its text.
func (j *rubricJudge) sample(ctx context.Context, prompt string) (string, error) {
	req := &model.LLMRequest{
		Model:    j.opts.JudgeModel,
		Contents: []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{},
	}
	var b strings.Builder
	for resp, err := range j.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if resp.ErrorCode != "" {
			return "", fmt.Errorf("judge error %s: %s", resp.ErrorCode, resp.ErrorMessage)
		}
		if t := contentText(resp.Content); t != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(t)
		}
	}
	return b.String(), nil
}

// summarize combines per-turn results into the case result: the score is the
// mean of every rubric verdict across turns, and each rubric's overall score
// is its mean across turns.
func (j *rubricJudge) summarize(perInvocation []InvocationResult) MetricResult {
	var all []*float64
	byID := map[string][]*float64{}
	var order []string
	for _, r := range perInvocation {
		for _, rs := range r.RubricScores {
			if _, ok := byID[rs.RubricID]; !ok {
				order = append(order, rs.RubricID)
			}
			byID[rs.RubricID] = append(byID[rs.RubricID], rs.Score)
			all = append(all, rs.Score)
		}
	}
	var overall []RubricScore
	for _, id := range order {
		overall = append(overall, RubricScore{RubricID: id, Score: mean(byID[id]), Rationale: ptr(aggregatedRationale)})
	}
	score := mean(all)
	return MetricResult{
		Score:         score,
		Status:        statusFor(score, j.threshold),
		RubricScores:  overall,
		PerInvocation: perInvocation,
	}
}

// formatRubrics lists rubrics the way the prompts expect.
func formatRubrics(rubrics []Rubric) string {
	lines := make([]string, len(rubrics))
	for i, r := range rubrics {
		lines[i] = fmt.Sprintf("*  [id: %s] %s", r.RubricID, r.Text())
	}
	return strings.Join(lines, "\n")
}

// verdict is one rubric's answer parsed from a judge reply.
type verdict struct {
	rubricID  string
	property  string
	rationale string
	score     *float64
}

var (
	idPattern        = regexp.MustCompile(`(?m)^\s*ID: (.*)$`)
	propertyPattern  = regexp.MustCompile(`(?m)^\s*Property: (.*)$`)
	rationalePattern = regexp.MustCompile(`Rationale: (.*)`)
	verdictPattern   = regexp.MustCompile(`Verdict: (.*)`)
)

// parseVerdicts reads the ID/Property/Rationale/Verdict blocks from a judge
// reply. If the counts of properties, rationales and verdicts differ it
// returns nothing, since a partial parse could drop a failed rubric and inflate
// the score.
func parseVerdicts(text string) []verdict {
	props := propertyPattern.FindAllStringSubmatchIndex(text, -1)
	ids := idPattern.FindAllStringSubmatchIndex(text, -1)
	rationales := rationalePattern.FindAllStringSubmatch(text, -1)
	verdicts := verdictPattern.FindAllStringSubmatch(text, -1)
	if len(props) != len(rationales) || len(props) != len(verdicts) {
		return nil
	}

	out := make([]verdict, len(props))
	for i, p := range props {
		// Take the id line just before this property, not the i-th id, so a
		// missing id line can't shift ids onto the wrong property.
		prevStart := -1
		if i > 0 {
			prevStart = props[i-1][0]
		}
		var id string
		for _, m := range ids {
			if prevStart < m[0] && m[0] < p[0] {
				id = strings.TrimSpace(text[m[2]:m[3]])
			}
		}
		out[i] = verdict{
			rubricID:  id,
			property:  strings.TrimSpace(text[p[2]:p[3]]),
			rationale: strings.TrimSpace(rationales[i][1]),
			score:     verdictScore(verdicts[i][1]),
		}
	}
	return out
}

func verdictScore(v string) *float64 {
	v = strings.ToLower(v)
	switch {
	case strings.Contains(v, "yes"):
		return ptr(1.0)
	case strings.Contains(v, "no"):
		return ptr(0.0)
	}
	return nil
}

// matchRubrics maps verdicts to rubrics, by id or else by property text.
// Verdicts for unknown rubrics are dropped.
func matchRubrics(verdicts []verdict, rubrics []Rubric) []RubricScore {
	byID := map[string]Rubric{}
	byText := map[string]Rubric{}
	for _, r := range rubrics {
		byID[r.RubricID] = r
		byText[normalizeText(r.Text())] = r
	}
	var out []RubricScore
	for _, v := range verdicts {
		r, ok := byID[v.rubricID]
		if !ok || v.rubricID == "" {
			r, ok = byText[normalizeText(v.property)]
		}
		if !ok {
			continue
		}
		out = append(out, RubricScore{RubricID: r.RubricID, Rationale: ptr(v.rationale), Score: v.score})
	}
	return out
}

// majorityVote combines samples per rubric. More yes than no votes gives yes;
// otherwise, if there was any vote, no. A rubric with no parseable verdict in
// any sample keeps a null score, as in adk-python.
func majorityVote(samples [][]RubricScore) []RubricScore {
	type tally struct{ none, yes, no []RubricScore }
	tallies := map[string]*tally{}
	var order []string
	for _, sample := range samples {
		for _, rs := range sample {
			t, ok := tallies[rs.RubricID]
			if !ok {
				t = &tally{}
				tallies[rs.RubricID] = t
				order = append(order, rs.RubricID)
			}
			switch {
			case rs.Score == nil:
				t.none = append(t.none, rs)
			case *rs.Score == 1:
				t.yes = append(t.yes, rs)
			default:
				t.no = append(t.no, rs)
			}
		}
	}
	out := make([]RubricScore, 0, len(order))
	for _, id := range order {
		t := tallies[id]
		switch {
		case len(t.yes) == 0 && len(t.no) == 0:
			out = append(out, t.none[0])
		case len(t.yes) > len(t.no):
			out = append(out, t.yes[0])
		default:
			out = append(out, t.no[0])
		}
	}
	return out
}

func rubricScoreValues(scores []RubricScore) []*float64 {
	vals := make([]*float64, len(scores))
	for i, s := range scores {
		vals[i] = s.Score
	}
	return vals
}

var smartChars = strings.NewReplacer(
	"‘", "'", "’", "'", "“", `"`, "”", `"`, "–", "-", "—", "-",
)

const decorationChars = " *_`#>-•\"'"

// normalizeText undoes the markdown and typography judges add when they echo
// a rubric back, so it still matches the rubric text.
func normalizeText(s string) string {
	s = smartChars.Replace(norm.NFKC.String(s))
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	return strings.ToLower(strings.Trim(s, decorationChars))
}
