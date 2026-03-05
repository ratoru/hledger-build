// Package varsubst provides string template variable substitution ({year}, {basename}, etc.).
package varsubst

import "strings"

// Apply replaces {key} placeholders in tmpl with values from vars.
// Unknown placeholders are left unchanged.
func Apply(tmpl string, vars map[string]string) string {
	args := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		args = append(args, "{"+k+"}", v)
	}
	return strings.NewReplacer(args...).Replace(tmpl)
}
