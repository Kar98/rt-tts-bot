package adkeval

import (
	"encoding/json"
	"strings"
	"unicode"

	"google.golang.org/genai"
)

// adk-python writes genai objects with snake_case keys (function_call,
// mime_type), but genai Go tags them camelCase. The types at the end of this
// file wrap the genai types this package stores and convert the keys on the
// way in and out.

// opaqueKeys hold user data, so nothing under them is renamed. Keys are in
// snake_case form.
var opaqueKeys = map[string]bool{
	"args":                   true,
	"partial_args":           true,
	"response":               true,
	"parameters_json_schema": true,
	"response_json_schema":   true,
	"default":                true,
	"example":                true,
}

// nameMapKeys map user-chosen names (schema property names) to objects whose
// own keys are still renamed.
var nameMapKeys = map[string]bool{"properties": true}

func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func camelToSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renameKeys returns v with every object key passed through rename, except
// under opaqueKeys and for the keys of nameMapKeys objects.
func renameKeys(v any, rename func(string) string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			snake := camelToSnake(k)
			props, isNameMap := val.(map[string]any)
			switch {
			case opaqueKeys[snake]:
				out[rename(k)] = val
			case nameMapKeys[snake] && isNameMap:
				renamed := make(map[string]any, len(props))
				for name, schema := range props {
					renamed[name] = renameKeys(schema, rename)
				}
				out[rename(k)] = renamed
			default:
				out[rename(k)] = renameKeys(val, rename)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = renameKeys(val, rename)
		}
		return out
	default:
		return v
	}
}

// marshalSnake marshals a genai value with snake_case keys.
func marshalSnake(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return nil, err
	}
	return json.Marshal(renameKeys(tree, camelToSnake))
}

// unmarshalSnake reads a genai value written with snake_case keys. camelCase
// keys pass through unchanged, so both forms load.
func unmarshalSnake(b []byte, v any) error {
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return err
	}
	camel, err := json.Marshal(renameKeys(tree, snakeToCamel))
	if err != nil {
		return err
	}
	return json.Unmarshal(camel, v)
}

// Content is a genai.Content that reads and writes adk-python's JSON.
type Content genai.Content

// NewContent converts c without copying it.
func NewContent(c *genai.Content) *Content { return (*Content)(c) }

// GenAI converts c back without copying it.
func (c *Content) GenAI() *genai.Content { return (*genai.Content)(c) }

func (c Content) MarshalJSON() ([]byte, error)  { return marshalSnake(genai.Content(c)) }
func (c *Content) UnmarshalJSON(b []byte) error { return unmarshalSnake(b, (*genai.Content)(c)) }

// Part is a genai.Part that reads and writes adk-python's JSON.
type Part genai.Part

func (p Part) MarshalJSON() ([]byte, error)  { return marshalSnake(genai.Part(p)) }
func (p *Part) UnmarshalJSON(b []byte) error { return unmarshalSnake(b, (*genai.Part)(p)) }

// FunctionCall is a genai.FunctionCall that reads and writes adk-python's JSON.
type FunctionCall genai.FunctionCall

func (f FunctionCall) MarshalJSON() ([]byte, error) { return marshalSnake(genai.FunctionCall(f)) }
func (f *FunctionCall) UnmarshalJSON(b []byte) error {
	return unmarshalSnake(b, (*genai.FunctionCall)(f))
}

// FunctionResponse is a genai.FunctionResponse that reads and writes
// adk-python's JSON.
type FunctionResponse genai.FunctionResponse

func (f FunctionResponse) MarshalJSON() ([]byte, error) {
	return marshalSnake(genai.FunctionResponse(f))
}
func (f *FunctionResponse) UnmarshalJSON(b []byte) error {
	return unmarshalSnake(b, (*genai.FunctionResponse)(f))
}

// UsageMetadata is a genai.GenerateContentResponseUsageMetadata that reads and
// writes adk-python's JSON.
type UsageMetadata genai.GenerateContentResponseUsageMetadata

func (u UsageMetadata) MarshalJSON() ([]byte, error) {
	return marshalSnake(genai.GenerateContentResponseUsageMetadata(u))
}
func (u *UsageMetadata) UnmarshalJSON(b []byte) error {
	return unmarshalSnake(b, (*genai.GenerateContentResponseUsageMetadata)(u))
}

// Tool is a genai.Tool that reads and writes adk-python's JSON.
type Tool genai.Tool

func (t Tool) MarshalJSON() ([]byte, error)  { return marshalSnake(genai.Tool(t)) }
func (t *Tool) UnmarshalJSON(b []byte) error { return unmarshalSnake(b, (*genai.Tool)(t)) }

// contentText joins the text parts of c with newlines, as adk-python's
// get_text_from_content does. Thoughts are left out.
func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var texts []string
	for _, p := range c.Parts {
		if p != nil && p.Text != "" && !p.Thought {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
