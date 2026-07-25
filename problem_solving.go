package skawld

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

const problemSolvingSystemHeader = "Problem-solving run state:"

type problemRunState struct {
	mu                sync.Mutex
	inspected         map[string]struct{}
	modified          map[string]struct{}
	verification      map[string]struct{}
	recentErrors      []string
	consecutiveErrors int
	repoMapped        bool
}

func newProblemRunState() *problemRunState {
	return &problemRunState{
		inspected:    map[string]struct{}{},
		modified:     map[string]struct{}{},
		verification: map[string]struct{}{},
	}
}

func (s *problemRunState) recordToolCall(cwd, toolName string, input map[string]interface{}, isError bool, content interface{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if isError {
		s.consecutiveErrors++
		s.recentErrors = appendBounded(s.recentErrors, fmt.Sprintf("%s: %s", toolName, firstLine(fmt.Sprint(content))), 5)
	} else {
		s.consecutiveErrors = 0
	}
	switch toolName {
	case "Read":
		if path := inputPath(cwd, input); path != "" && !isError {
			s.inspected[path] = struct{}{}
		}
	case "RepoMap":
		if !isError {
			s.repoMapped = true
		}
	case "Write", "Edit":
		if path := inputPath(cwd, input); path != "" && !isError {
			s.modified[path] = struct{}{}
			s.verification[path] = struct{}{}
		}
	case "Bash":
		if !isError && looksLikeVerification(input) {
			s.verification = map[string]struct{}{}
		}
	}
}

func (s *problemRunState) systemText(opts ProblemSolvingOptions) string {
	if s == nil || !opts.Enabled {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var lines []string
	lines = append(lines, problemSolvingSystemHeader)
	if opts.AutoRepoMap && !s.repoMapped {
		lines = append(lines, "- Repo map has not been requested yet; call RepoMap early when package layout or test scope is unclear.")
	}
	if opts.RequirePlanBeforeWrite && len(s.modified) == 0 {
		lines = append(lines, "- Before Write/Edit, state the intended change in assistant text or create/update a task.")
	}
	if len(s.inspected) > 0 {
		lines = append(lines, "- Inspected files: "+strings.Join(sortedKeys(s.inspected, 8), ", "))
	}
	if len(s.modified) > 0 {
		lines = append(lines, "- Modified files: "+strings.Join(sortedKeys(s.modified, 8), ", "))
	}
	if opts.AutoVerify && len(s.verification) > 0 {
		lines = append(lines, "- Verification needed for: "+strings.Join(sortedKeys(s.verification, 8), ", "))
		lines = append(lines, "- Suggested Go verification: gofmt touched .go files, then targeted go test, then go test ./... when shared behavior changed.")
	}
	if s.consecutiveErrors >= opts.MaxConsecutiveToolErrors && len(s.recentErrors) > 0 {
		lines = append(lines, "- Repeated tool errors detected; change strategy instead of retrying the same call.")
	}
	if len(s.recentErrors) > 0 {
		lines = append(lines, "- Recent tool errors: "+strings.Join(s.recentErrors, " | "))
	}
	return strings.Join(lines, "\n")
}

func inputPath(cwd string, input map[string]interface{}) string {
	raw, _ := input["file_path"].(string)
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	rel, err := filepath.Rel(cwd, filepath.Clean(path))
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func looksLikeVerification(input map[string]interface{}) bool {
	command, _ := input["command"].(string)
	command = strings.ToLower(command)
	checks := []string{"go test", "go vet", "gofmt", "go build", "npm test", "pytest", "cargo test", "make test"}
	for _, check := range checks {
		if strings.Contains(command, check) {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]struct{}, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = append(keys[:limit], fmt.Sprintf("... +%d more", len(keys)-limit))
	}
	return keys
}

func appendBounded(values []string, value string, limit int) []string {
	values = append(values, value)
	if limit > 0 && len(values) > limit {
		return values[len(values)-limit:]
	}
	return values
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "tool returned an error"
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 120 {
		text = text[:120] + "..."
	}
	return text
}

func appendProblemSolvingSystemBlock(system []core.SystemBlock, text string) []core.SystemBlock {
	if strings.TrimSpace(text) == "" {
		return system
	}
	out := append([]core.SystemBlock(nil), system...)
	out = append(out, core.SystemBlock{Type: "text", Text: text})
	return out
}
