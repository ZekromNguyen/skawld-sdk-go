package tools

import (
	"testing"
)

func TestWebSearchValidate(t *testing.T) {
	tool := WebSearchTool{}

	// Missing query
	_, err := tool.Validate(map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing query")
	}

	// Valid query
	result, err := tool.Validate(map[string]interface{}{"query": "go testing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["query"] != "go testing" {
		t.Errorf("query = %v, want 'go testing'", result["query"])
	}
	if result["limit"] != 10 {
		t.Errorf("limit = %v, want 10", result["limit"])
	}

	// Custom limit
	result, err = tool.Validate(map[string]interface{}{"query": "test", "limit": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["limit"] != 5 {
		t.Errorf("limit = %v, want 5", result["limit"])
	}

	// Limit clamped
	result, err = tool.Validate(map[string]interface{}{"query": "test", "limit": 50})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["limit"] != 20 {
		t.Errorf("limit = %v, want 20 (clamped)", result["limit"])
	}
}

func TestWebFetchValidate(t *testing.T) {
	tool := WebFetchTool{}

	// Missing url
	_, err := tool.Validate(map[string]interface{}{})
	if err == nil {
		t.Error("expected error for missing url")
	}

	// Valid URL
	result, err := tool.Validate(map[string]interface{}{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["url"] != "https://example.com" {
		t.Errorf("url = %v, want 'https://example.com'", result["url"])
	}

	// Invalid URL
	_, err = tool.Validate(map[string]interface{}{"url": ":::broken"})
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestWebSearchSummarize(t *testing.T) {
	tool := WebSearchTool{}
	s := tool.Summarize(map[string]interface{}{"query": "go generics"})
	if s != "Search: go generics" {
		t.Errorf("Summarize = %q, want 'Search: go generics'", s)
	}
}

func TestWebFetchSummarize(t *testing.T) {
	tool := WebFetchTool{}
	s := tool.Summarize(map[string]interface{}{"url": "https://example.com"})
	if s != "Fetch: https://example.com" {
		t.Errorf("Summarize = %q, want 'Fetch: https://example.com'", s)
	}
}

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"<b>bold</b>", "bold"},
		{"<a href=\"foo\">link</a>", "link"},
		{"no tags here", "no tags here"},
		{"<p>Hello <em>world</em></p>", "Hello world"},
	}
	for _, tt := range tests {
		result := stripHTMLTags(tt.input)
		if result != tt.expect {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, result, tt.expect)
		}
	}
}

func TestHTMLUnescape(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"&", "&"},
		{"<tag>", "<tag>"},
		{"“hello”", "\"hello\""},
		{"plain text", "plain text"},
	}
	for _, tt := range tests {
		result := htmlUnescape(tt.input)
		if result != tt.expect {
			t.Errorf("htmlUnescape(%q) = %q, want %q", tt.input, result, tt.expect)
		}
	}
}

func TestCollapseWhitespace(t *testing.T) {
	result := collapseWhitespace("  hello   world  ")
	if result != "hello world" {
		t.Errorf("collapseWhitespace = %q, want 'hello world'", result)
	}
}

func TestParseDDGResultsEmpty(t *testing.T) {
	results := parseDDGResults("<html><body>No results</body></html>", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty HTML, got %d", len(results))
	}
}

func TestParseDDGResults(t *testing.T) {
	html := `
	<a rel="nofollow" class="result__a" href="https://example.com">Example</a>
	<a class="result__snippet">This is a snippet</a>
	<a rel="nofollow" class="result__a" href="https://go.dev">Go Programming</a>
	<a class="result__snippet">The Go programming language</a>
	`
	results := parseDDGResults(html, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Example" {
		t.Errorf("result[0].Title = %q, want 'Example'", results[0].Title)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("result[0].URL = %q, want 'https://example.com'", results[0].URL)
	}
	if results[1].Title != "Go Programming" {
		t.Errorf("result[1].Title = %q, want 'Go Programming'", results[1].Title)
	}
}

func TestParseDDGResultsLimit(t *testing.T) {
	html := `
	<a rel="nofollow" class="result__a" href="https://a.com">A</a>
	<a class="result__snippet">Snip A</a>
	<a rel="nofollow" class="result__a" href="https://b.com">B</a>
	<a class="result__snippet">Snip B</a>
	<a rel="nofollow" class="result__a" href="https://c.com">C</a>
	<a class="result__snippet">Snip C</a>
	`
	results := parseDDGResults(html, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (limit), got %d", len(results))
	}
}
