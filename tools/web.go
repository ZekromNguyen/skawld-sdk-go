package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

// ─── WebSearch Tool ─────────────────────────────────────────────────────

// WebSearchTool searches the web using DuckDuckGo HTML and returns results.
type WebSearchTool struct{}

func (WebSearchTool) Name() string        { return "WebSearch" }
func (WebSearchTool) Description() string {
	return "Search the web using a search query. Returns a list of results with titles, URLs, and snippets."
}
func (WebSearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query string",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results (default 10, max 20)",
				"default":     10,
			},
		},
		"required": []string{"query"},
	}
}
func (WebSearchTool) Scope() core.ToolScope       { return core.ToolScopeRead }
func (WebSearchTool) ParallelSafe() bool          { return true }

func (t WebSearchTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	query, _ := asString(raw["query"])
	if query == "" {
		return nil, core.NewToolExecutionError("WebSearch", "query is required")
	}
	limit := asInt(raw["limit"], 10)
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}
	return map[string]interface{}{
		"query": query,
		"limit": limit,
	}, nil
}

func (t WebSearchTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	query, _ := input["query"].(string)
	limit := asInt(input["limit"], 10)

	results, err := duckDuckGoSearch(ctx.Context, query, limit)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("WebSearch error: %v", err), IsError: true}, nil
	}

	if len(results) == 0 {
		return core.ToolResult{Content: "No results found.", Summary: "WebSearch: 0 results"}, nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return core.ToolResult{
		Content: sb.String(),
		Summary: fmt.Sprintf("WebSearch: %d results for %q", len(results), truncate(query, 40)),
	}, nil
}

func (t WebSearchTool) Summarize(input map[string]interface{}) string {
	query, _ := input["query"].(string)
	return fmt.Sprintf("Search: %s", truncate(query, 60))
}

// ─── WebFetch Tool ───────────────────────────────────────────────────────

// WebFetchTool fetches content from a URL and returns the extracted text.
type WebFetchTool struct{}

func (WebFetchTool) Name() string        { return "WebFetch" }
func (WebFetchTool) Description() string {
	return "Fetch content from a URL. Returns the page text content, stripping HTML tags. Useful for reading web pages or API responses."
}
func (WebFetchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The URL to fetch",
			},
			"max_length": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum content length in characters (default 5000)",
				"default":     5000,
			},
		},
		"required": []string{"url"},
	}
}
func (WebFetchTool) Scope() core.ToolScope       { return core.ToolScopeRead }
func (WebFetchTool) ParallelSafe() bool          { return true }

func (t WebFetchTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	u, _ := asString(raw["url"])
	if u == "" {
		return nil, core.NewToolExecutionError("WebFetch", "url is required")
	}
	if _, err := url.Parse(u); err != nil {
		return nil, core.NewToolExecutionError("WebFetch", fmt.Sprintf("invalid URL: %v", err))
	}
	maxLen := asInt(raw["max_length"], 5000)
	if maxLen < 100 {
		maxLen = 100
	}
	return map[string]interface{}{
		"url":        u,
		"max_length": maxLen,
	}, nil
}

func (t WebFetchTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	u, _ := input["url"].(string)
	maxLen := asInt(input["max_length"], 5000)

	content, err := fetchURL(ctx.Context, u, maxLen)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("WebFetch error: %v", err), IsError: true}, nil
	}

	return core.ToolResult{
		Content: content,
		Summary: fmt.Sprintf("WebFetch: %s (%d chars)", truncate(u, 50), len(content)),
	}, nil
}

func (t WebFetchTool) Summarize(input map[string]interface{}) string {
	u, _ := input["url"].(string)
	return fmt.Sprintf("Fetch: %s", truncate(u, 60))
}

// ─── HTTP Client & Parsing ───────────────────────────────────────────────

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// SearchResult holds one search result.
type SearchResult struct {
	Title    string
	URL      string
	Snippet  string
}

// duckDuckGoSearch queries DuckDuckGo HTML and parses results.
func duckDuckGoSearch(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Raven/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return parseDDGResults(string(body), limit), nil
}

// parseDDGResults extracts search results from DuckDuckGo HTML.
func parseDDGResults(html string, limit int) []SearchResult {
	var results []SearchResult

	// Match result blocks: <a rel="nofollow" class="result__a" href="URL">Title</a>
	reResult := regexp.MustCompile(`class="result__a"\s+href="([^"]+)"[^>]*>(.*?)</a>`)
	// Match snippets: <a class="result__snippet" ...>Snippet</a>
	reSnippet := regexp.MustCompile(`class="result__snippet"[^>]*>(.*?)</a>`)

	matches := reResult.FindAllStringSubmatch(html, -1)
	snippets := reSnippet.FindAllStringSubmatch(html, -1)

	for i, m := range matches {
		if i >= limit {
			break
		}
		link := htmlUnescape(m[1])
		title := stripHTMLTags(m[2])
		snippet := ""
		if i < len(snippets) {
			snippet = stripHTMLTags(snippets[i][1])
		}

		// DuckDuckGo redirects via /uddg= — extract the actual URL
		if strings.HasPrefix(link, "//duckduckgo.com/l/?uddg=") {
			if idx := strings.Index(link, "&"); idx > 0 {
				encoded := link[strings.Index(link, "=")+1 : idx]
				if decoded, err := url.QueryUnescape(encoded); err == nil {
					link = decoded
				}
			}
		}

		results = append(results, SearchResult{
			Title:   title,
			URL:     link,
			Snippet: snippet,
		})
	}

	return results
}

// fetchURL retrieves and extracts text content from a URL.
func fetchURL(ctx context.Context, u string, maxLen int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Raven/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	content := stripHTMLTags(string(body))
	content = collapseWhitespace(content)

	if len(content) > maxLen {
		content = content[:maxLen] + fmt.Sprintf("\n... truncated (%d chars omitted)", len(content)-maxLen)
	}

	return content, nil
}

// ─── HTML Utilities ──────────────────────────────────────────────────────

var reHTMLTag = regexp.MustCompile(`<[^>]*>`)
var reWhitespace = regexp.MustCompile(`\s+`)

func stripHTMLTags(s string) string {
	return reHTMLTag.ReplaceAllString(s, "")
}

func htmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&", "&")
	s = strings.ReplaceAll(s, "<", "<")
	s = strings.ReplaceAll(s, ">", ">")
	s = strings.ReplaceAll(s, "“", `"`)
	s = strings.ReplaceAll(s, "”", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return s
}

func collapseWhitespace(s string) string {
	return strings.TrimSpace(reWhitespace.ReplaceAllString(s, " "))
}
