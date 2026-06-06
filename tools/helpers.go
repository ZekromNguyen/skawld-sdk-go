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
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
	}
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

func terminateProcessTree(cmd *exec.Cmd, done <-chan error, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	taskTerminateTree(cmd.Process.Pid)
	select {
	case err := <-done:
		return err
	case <-time.After(grace):
		taskKillTree(cmd.Process.Pid)
		return <-done
	}
}

func taskTerminateTree(pid int) {
	if pid <= 0 {
		return
	}
	if os.PathSeparator == '\\' {
		_ = exec.Command("taskkill", "/pid", fmt.Sprint(pid), "/t").Run()
		return
	}
	_ = exec.Command("kill", "-TERM", fmt.Sprintf("-%d", pid)).Run()
}

func taskKillTree(pid int) {
	if pid <= 0 {
		return
	}
	if os.PathSeparator == '\\' {
		_ = exec.Command("taskkill", "/pid", fmt.Sprint(pid), "/t", "/f").Run()
		return
	}
	_ = exec.Command("kill", "-KILL", fmt.Sprintf("-%d", pid)).Run()
}
