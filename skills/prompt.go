package skills

import (
	"fmt"
	"strings"
)

func ListingPrompt(defs []Definition) string {
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available skills:\n\n")
	for _, def := range defs {
		b.WriteString("- ")
		b.WriteString(def.Name)
		if def.Description != "" {
			b.WriteString(": ")
			b.WriteString(def.Description)
		}
		if def.WhenToUse != "" {
			b.WriteString("\n  When to use: ")
			b.WriteString(def.WhenToUse)
		}
		if def.ArgumentHint != "" {
			b.WriteString("\n  Argument hint: ")
			b.WriteString(def.ArgumentHint)
		}
		if len(def.AllowedTools) > 0 {
			b.WriteString("\n  Allowed tools: ")
			b.WriteString(strings.Join(def.AllowedTools, ", "))
		}
		if def.Model != "" {
			b.WriteString("\n  Model: ")
			b.WriteString(string(def.Model))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nUse the Skill tool to invoke one of these skills when it is relevant.")
	return b.String()
}

func Substitute(def Definition, arguments string) string {
	args := strings.TrimSpace(arguments)
	body := def.Body
	rawReplacements := []string{"$ARGUMENTS", "{{arguments}}", "{{ argument }}"}
	replaced := false
	for _, marker := range rawReplacements {
		if strings.Contains(body, marker) {
			body = strings.ReplaceAll(body, marker, args)
			replaced = true
		}
	}
	jsonReplacements := []string{"{{arguments_json}}", "{{ arguments_json }}"}
	for _, marker := range jsonReplacements {
		if strings.Contains(body, marker) {
			body = strings.ReplaceAll(body, marker, argumentsJSON(args))
			replaced = true
		}
	}
	if args != "" && !replaced {
		body += fmt.Sprintf("\n\nArguments:\n%s", args)
	}
	return strings.TrimSpace(body)
}
