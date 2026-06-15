package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestXSearchValidate(t *testing.T) {
	tool := XSearchTool{}

	input, err := tool.Validate(map[string]interface{}{
		"query": "test query",
		"limit": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["query"] != "test query" || input["limit"] != 5 {
		t.Fatalf("unexpected input: %+v", input)
	}

	// Test limit clamping
	input, err = tool.Validate(map[string]interface{}{
		"query": "test",
		"limit": 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["limit"] != 50 {
		t.Fatalf("expected limit clamped to 50, got %v", input["limit"])
	}

	// Test missing query
	_, err = tool.Validate(map[string]interface{}{"limit": 10})
	if err == nil {
		t.Fatal("expected error for missing query")
	}

	// Verify summary
	summary := tool.Summarize(map[string]interface{}{"query": "test query"})
	if !strings.Contains(summary, "test query") {
		t.Fatalf("unexpected summary: %s", summary)
	}
}

func TestXSearchMissingAPIKey(t *testing.T) {
	// Ensure XAI_API_KEY is not set
	oldKey := os.Getenv("XAI_API_KEY")
	os.Unsetenv("XAI_API_KEY")
	defer func() {
		if oldKey != "" {
			os.Setenv("XAI_API_KEY", oldKey)
		}
	}()

	tool := XSearchTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"query": "test query",
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when XAI_API_KEY is not set")
	}
	content := fmt.Sprint(res.Content)
	if !strings.Contains(content, "XAI_API_KEY") {
		t.Fatalf("expected XAI_API_KEY mention in error: %s", content)
	}
}

func TestParseXAIPosts(t *testing.T) {
	text := `@alice Check out the new release! It's amazing. https://x.com/alice/status/123
@bob I agree, the performance improvements are substantial
A standalone URL https://x.com/bob/status/456
just some random text without a handle`

	results := parseXAIPosts(text, 10)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Check first result
	if results[0].Author != "alice" {
		t.Errorf("expected author=alice, got %q", results[0].Author)
	}
	if results[0].URL != "https://x.com/alice/status/123" {
		t.Errorf("expected URL, got %q", results[0].URL)
	}
}

func TestParseXAIPostsLimit(t *testing.T) {
	text := "@a test1\n@b test2\n@c test3\n@d test4\n@e test5\n@f test6"
	results := parseXAIPosts(text, 3)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestXSearchParallelSafe(t *testing.T) {
	tool := XSearchTool{}
	if !tool.ParallelSafe() {
		t.Error("XSearch should be parallel safe")
	}
}

func TestXSearchScope(t *testing.T) {
	tool := XSearchTool{}
	if tool.Scope() != core.ToolScopeRead {
		t.Error("XSearch should be read-only scope")
	}
}