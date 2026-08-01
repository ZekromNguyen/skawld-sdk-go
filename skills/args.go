package skills

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SplitArguments parses a shell-like argument string into tokens.
func SplitArguments(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	inToken := false
	escaped := false
	for _, ch := range input {
		if escaped {
			current.WriteRune(ch)
			inToken = true
			escaped = false
			continue
		}
		if quote != '\'' && ch == '\\' {
			escaped = true
			inToken = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			inToken = true
			continue
		}
		switch {
		case ch == '\'' || ch == '"':
			quote = ch
			inToken = true
		case isArgumentSpace(ch):
			if inToken {
				args = append(args, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteRune(ch)
			inToken = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted argument")
	}
	if inToken {
		args = append(args, current.String())
	}
	return args, nil
}

func argumentsJSON(input string) string {
	args, err := SplitArguments(input)
	if err != nil {
		args = []string{input}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func isArgumentSpace(ch rune) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}
