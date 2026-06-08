package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkGrepFallbackLargeTree(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 400; i++ {
		path := filepath.Join(dir, "pkg", strconv.Itoa(i), "file.go")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		content := strings.Repeat("package p\nfunc x() {}\n", 20)
		if i%10 == 0 {
			content += "// TODO benchmark\n"
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	tool := GrepTool{}
	input, err := tool.Validate(map[string]interface{}{"pattern": "TODO", "output_mode": "content", "-n": true, "head_limit": 50})
	if err != nil {
		b.Fatal(err)
	}
	ctx := makeCtx(dir)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := runGrepFallback(ctx, input, dir); err != nil {
			b.Fatal(err)
		}
	}
}
