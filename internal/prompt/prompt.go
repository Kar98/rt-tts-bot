// Package prompt fills placeholders in agent instruction templates.
package prompt

import "strings"

// Render replaces each "<key>" in tmpl with vars[key]. It uses angle brackets
// because ADK reads "{key}" in an instruction as a session state placeholder.
func Render(tmpl string, vars map[string]string) string {
	for k, v := range vars {
		tmpl = strings.ReplaceAll(tmpl, "<"+k+">", v)
	}
	return tmpl
}
