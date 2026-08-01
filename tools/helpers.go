package tools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func resolvePath(input, cwd string) string {
	if filepath.IsAbs(input) {
		return filepath.Clean(input)
	}
	return filepath.Clean(filepath.Join(cwd, input))
}

func numberedLines(text string, offset int) string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		out = append(out, fmt.Sprintf("%d\t%s", offset+i, line))
	}
	return strings.Join(out, "\n")
}

func formatNumberedLine(line string, lineNo int) string {
	line = strings.TrimSuffix(line, "\r")
	if len(line) > 2000 {
		omitted := len(line) - 2000
		line = line[:2000] + fmt.Sprintf("... (%d chars truncated)", omitted)
	}
	return fmt.Sprintf("%d\t%s", lineNo, line)
}

func readLineRange(ctx context.Context, path string, offset, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	lineNo := 0
	out := make([]string, 0, limit)
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		if line == "" && err == io.EOF {
			break
		}
		lineNo++
		line = strings.TrimSuffix(line, "\n")
		if lineNo >= offset {
			out = append(out, formatNumberedLine(line, lineNo))
		}
		if err == io.EOF {
			break
		}
	}
	return strings.Join(out, "\n"), nil
}

func truncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	return s[:cap] + fmt.Sprintf("... (%d chars truncated)", len(s)-cap)
}

func displayPath(cwd, path string) string {
	rel, err := filepath.Rel(cwd, path)
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func editHint(text, oldString string) string {
	oldString = strings.TrimSpace(oldString)
	if oldString == "" {
		return "hint: old_string is empty after trimming; read the target region and include exact surrounding context."
	}
	lines := strings.Split(text, "\n")
	needle := firstNonEmptyLine(oldString)
	if needle == "" {
		return "hint: read the target region again and copy the exact old_string including whitespace."
	}
	needleWords := strings.Fields(needle)
	for i, line := range lines {
		if strings.Contains(line, needle) || strings.Contains(strings.TrimSpace(line), strings.TrimSpace(needle)) || lineSharesWords(line, needleWords) {
			start := max(0, i-2)
			end := min(len(lines), i+3)
			var b strings.Builder
			b.WriteString("hint: no exact match. Nearby candidate:\n")
			for j := start; j < end; j++ {
				b.WriteString(fmt.Sprintf("%d\t%s\n", j+1, strings.TrimSuffix(lines[j], "\r")))
			}
			return strings.TrimRight(b.String(), "\n")
		}
	}
	return "hint: read the file or use Grep to find the current text before retrying Edit."
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func lineSharesWords(line string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	line = strings.ToLower(line)
	matches := 0
	for _, word := range words {
		word = strings.ToLower(strings.Trim(word, "\"'`.,;:(){}[]"))
		if len(word) < 3 {
			continue
		}
		if strings.Contains(line, word) {
			matches++
		}
	}
	return matches > 0
}

func bashHint(command, output string, exitCode int) string {
	if exitCode == 0 {
		return ""
	}
	lowerOut := strings.ToLower(output)
	lowerCmd := strings.ToLower(command)
	switch {
	case strings.Contains(lowerOut, "no such file") || strings.Contains(lowerOut, "cannot find"):
		return "verify the path with Glob or Read before retrying"
	case strings.Contains(lowerOut, "permission denied") || strings.Contains(lowerOut, "access is denied"):
		return "check permissions or choose a non-privileged command"
	case strings.Contains(lowerOut, "command not found") || strings.Contains(lowerOut, "not recognized"):
		return "check whether the executable exists or use the repo's documented command"
	case strings.Contains(lowerCmd, "go test") && strings.Contains(lowerOut, "build failed"):
		return "inspect the first compile error and run a targeted package test after editing"
	case strings.Contains(lowerCmd, "go vet"):
		return "fix the first vet diagnostic, then rerun go vet ./..."
	default:
		return "inspect the first error line and change strategy before retrying"
	}
}

func isDevicePath(absPath string) bool {
	slash := filepath.ToSlash(absPath)
	devicePrefixes := []string{"/dev/zero", "/dev/random", "/dev/urandom", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/proc/"}
	for _, prefix := range devicePrefixes {
		if strings.HasPrefix(slash, prefix) {
			return true
		}
	}
	return false
}

func detectLineEnding(sample string) string {
	crlf := strings.Count(sample, "\r\n")
	bareLF := strings.Count(strings.ReplaceAll(sample, "\r\n", ""), "\n")
	if crlf > bareLF {
		return "\r\n"
	}
	return "\n"
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".skawld-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func hasNullByte(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

func asString(v interface{}) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func asBool(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v interface{}, def int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return def
	}
}

func executable(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}

func runCommand(ctx context.Context, cwd string, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			return stdout.String() + stderr.String(), exitCode, err
		}
	}
	return stdout.String() + stderr.String(), exitCode, nil
}

func globToRegexp(pattern string) (*regexp.Regexp, error) {
	pattern = filepath.ToSlash(pattern)
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '*' {
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
			continue
		}
		if ch == '?' {
			b.WriteString("[^/]")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(ch)))
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func globMatch(pattern, rel string) bool {
	re, err := globToRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(filepath.ToSlash(rel))
}

// terminateProcessTree asks a running command to exit gracefully, then
// force-kills it if it hasn't complied within grace. The wait channel
// (typically fed by cmd.Wait in a goroutine) is drained so callers can
// still observe the final exit. The function is guaranteed to return
// within at most 2*grace no matter what, and never panics.
//
// taskTerminateTree and taskKillTree live in bash_sys_posix.go and
// bash_sys_windows.go so the syscall details stay platform-specific.
func terminateProcessTree(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if grace <= 0 {
		// No graceful phase requested — go straight to SIGKILL.
		taskKillTree(cmd.Process.Pid)
		select {
		case err := <-done:
			return err
		case <-time.After(time.Second):
			return fmt.Errorf("process %d did not terminate after kill", cmd.Process.Pid)
		}
	}

	taskTerminateTree(cmd.Process.Pid)
	select {
	case err, ok := <-done:
		if !ok {
			return nil
		}
		return err
	case <-time.After(grace):
	}

	taskKillTree(cmd.Process.Pid)
	select {
	case err, ok := <-done:
		if !ok {
			return fmt.Errorf("process %d did not terminate after kill", cmd.Process.Pid)
		}
		return err
	case <-time.After(grace):
		return fmt.Errorf("process %d did not terminate after kill", cmd.Process.Pid)
	}
}
