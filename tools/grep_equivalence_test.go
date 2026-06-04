package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var grepCorpus = map[string]string{
	"src/foo.ts": strings.Join([]string{
		"export const foo = 1;",
		"// TODO: remove this",
		"export function add(a: number, b: number) {",
		"  return a + b;",
		"}",
		"// NOTE: this is fine",
		"export const bar = foo + 2;",
	}, "\n"),
	"src/bar.ts": strings.Join([]string{
		"import { foo } from './foo';",
		"export const baz = foo * 2;",
		"// TODO: write tests",
		"export default baz;",
	}, "\n"),
	"src/util.js": strings.Join([]string{
		"function util() { return 42; }",
		"module.exports = { util };",
	}, "\n"),
	"docs/readme.md": strings.Join([]string{
		"# Project",
		"This project exports foo and bar.",
		"TODO: add more docs",
	}, "\n"),
	"docs/notes.md": strings.Join([]string{
		"## Notes",
		"Some notes about the project.",
		"See src/ for implementation.",
	}, "\n"),
}

var grepFixtureDir string

func setupGrepFixture(t *testing.T) {
	var err error
	grepFixtureDir, err = os.MkdirTemp("", "skawld-grep-equiv-*")
	if err != nil {
		t.Fatal(err)
	}
	for rel, content := range grepCorpus {
		abs := filepath.Join(grepFixtureDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func cleanupGrepFixture() {
	if grepFixtureDir != "" {
		os.RemoveAll(grepFixtureDir)
	}
}

func getFallbackLines(t *testing.T, pattern string, args map[string]interface{}) []string {
	tool := GrepTool{}
	args["pattern"] = pattern
	args["path"] = grepFixtureDir
	input, err := tool.Validate(args)
	if err != nil {
		t.Fatal(err)
	}
	ctx := makeCtx(grepFixtureDir)
	out, err := runGrepFallback(ctx, input, grepFixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		return []string{}
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	return lines
}

func getRgLines(t *testing.T, pattern string, args map[string]interface{}) []string {
	tool := GrepTool{}
	args["pattern"] = pattern
	args["path"] = grepFixtureDir
	input, err := tool.Validate(args)
	if err != nil {
		t.Fatal(err)
	}
	ctx := makeCtx(grepFixtureDir)
	rg, ok := executable("rg")
	if !ok {
		t.Skip("rg not found")
	}
	rgArgs := buildRgArgs(input, grepFixtureDir)
	out, exitCode, err := runCommand(ctx.Context, grepFixtureDir, rg, rgArgs...)
	if err != nil && exitCode != 1 {
		t.Fatal(err)
	}
	if exitCode == 1 && strings.TrimSpace(out) == "" {
		return []string{}
	}
	prefix := grepFixtureDir + string(filepath.Separator)
	prefix = filepath.ToSlash(prefix)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	var cleaned []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		l = filepath.ToSlash(l)
		if strings.HasPrefix(l, prefix) {
			l = l[len(prefix):]
		}
		cleaned = append(cleaned, l)
	}
	return cleaned
}

func sliceContains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	// just set eq since sorted output isn't guaranteed identical ordering between rg and fb across files sometimes, though we can sort them.
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortLines(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

func assertEquiv(t *testing.T, name, pattern string, args map[string]interface{}) {
	t.Run(name, func(t *testing.T) {
		fb := getFallbackLines(t, pattern, args)
		rg := getRgLines(t, pattern, args)
		sortLines(fb)
		sortLines(rg)
		// For contextual grep (-C, -A, -B), rg inserts "--" which might differ in ordering if we sort, but for equality it's fine if we stip "--".
		var fbClean, rgClean []string
		for _, v := range fb {
			if v != "--" {
				fbClean = append(fbClean, v)
			}
		}
		for _, v := range rg {
			if v != "--" {
				rgClean = append(rgClean, v)
			}
		}

		if !sliceEq(fbClean, rgClean) {
			t.Errorf("Mismatch!\nFB:\n%v\n\nRG:\n%v", strings.Join(fbClean, "\n"), strings.Join(rgClean, "\n"))
		}
	})
}

func TestGrepFallbackEquivalence(t *testing.T) {
	setupGrepFixture(t)
	defer cleanupGrepFixture()

	assertEquiv(t, "plain files_with_matches", "TODO", map[string]interface{}{})
	assertEquiv(t, "count mode", "TODO", map[string]interface{}{"output_mode": "count"})
	assertEquiv(t, "case insensitive files", "todo", map[string]interface{}{"-i": true})
	assertEquiv(t, "content no flags", "TODO", map[string]interface{}{"output_mode": "content"})
	assertEquiv(t, "content line numbers", "TODO", map[string]interface{}{"output_mode": "content", "-n": true})
	assertEquiv(t, "glob filter files", "export", map[string]interface{}{"glob": "**/*.ts"})
	assertEquiv(t, "glob string type files", "TODO", map[string]interface{}{"glob": "**/*.md"})
	assertEquiv(t, "no matches", "ZZZNOMATCH_XYZ", map[string]interface{}{})
	assertEquiv(t, "case insensitive content", "todo", map[string]interface{}{"-i": true, "output_mode": "content"})
	assertEquiv(t, "context lines -C 1", "TODO", map[string]interface{}{"output_mode": "content", "-n": true, "-C": 1})
}
