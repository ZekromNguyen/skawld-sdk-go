package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

// ─── XSearch Tool ────────────────────────────────────────────────────────

// XSearchTool searches X/Twitter via the xAI search API. Requires XAI_API_KEY.
type XSearchTool struct{}

func (XSearchTool) Name() string { return "XSearch" }
func (XSearchTool) Description() string {
	return "Search X/Twitter posts using the xAI API. Requires XAI_API_KEY environment variable."
}
func (XSearchTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query string.",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum results, default 10, max 50.",
			},
		},
		"required": []string{"query"},
	}
}
func (XSearchTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (XSearchTool) ParallelSafe() bool    { return true }

func (t XSearchTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseXSearchInput(raw, t.Name())
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}

func (t XSearchTool) Summarize(input map[string]interface{}) string {
	query, _ := input["query"].(string)
	return fmt.Sprintf("X search: %s", truncate(query, 60))
}

func (t XSearchTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := xsearchInputFrom(input)

	apiKey := strings.TrimSpace(os.Getenv("XAI_API_KEY"))
	if apiKey == "" {
		return core.ToolResult{
			Content: "XSearch error: XAI_API_KEY environment variable is not set. Set your xAI API key to use X/Twitter search.",
			Summary: t.Summarize(input),
			IsError: true,
		}, nil
	}

	results, err := xAISearch(ctx.Context, apiKey, in.Query, in.Limit)
	if err != nil {
		return core.ToolResult{Content: "XSearch error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}

	if len(results) == 0 {
		return core.ToolResult{Content: "No X results found.", Summary: "XSearch: 0 results"}, nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s (@%s)\n   %s\n   %s\n", i+1, r.Title, r.Author, r.URL, r.Snippet))
	}

	return core.ToolResult{
		Content: sb.String(),
		Summary: fmt.Sprintf("XSearch: %d results for %q", len(results), truncate(in.Query, 40)),
	}, nil
}

// ─── xAI API Client ──────────────────────────────────────────────────────

// xAIResult represents a single X/Twitter post result.
type xAIResult struct {
	Title   string
	Author  string
	URL     string
	Snippet string
}

var xaiHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

func xAISearch(ctx context.Context, apiKey, query string, limit int) ([]xAIResult, error) {
	reqBody := map[string]interface{}{
		"model": "grok-2-search",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": fmt.Sprintf("Search X/Twitter for: %s. Return the most relevant posts with author, text snippet, and URL.", query),
			},
		},
		"max_tokens": 2000,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.x.ai/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "Raven/1.0")

	resp, err := xaiHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xAI request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("xAI returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, nil
	}

	return parseXAIPosts(result.Choices[0].Message.Content, limit), nil
}

// parseXAIPosts extracts X/Twitter posts from xAI response text.
func parseXAIPosts(text string, limit int) []xAIResult {
	var results []xAIResult

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if len(results) >= limit {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Try to extract structured results from the model output
		// Expected format: "Author: text — URL"
		var title, author, url, snippet string

		// Look for @handle pattern
		if idx := strings.Index(line, "@"); idx >= 0 {
			// Extract author
			rest := line[idx:]
			endIdx := strings.IndexAny(rest, " :")
			if endIdx > 0 {
				author = rest[1:endIdx]
			} else if len(rest) > 1 {
				author = rest[1:]
				if spaceIdx := strings.Index(author, " "); spaceIdx > 0 {
					author = author[:spaceIdx]
				}
			}

			// Try to extract URL
			if urlIdx := strings.Index(line, "https://x.com/"); urlIdx >= 0 {
				urlEnd := strings.IndexAny(line[urlIdx:], " \t\n")
				if urlEnd < 0 {
					urlEnd = len(line)
				} else {
					urlEnd = urlIdx + urlEnd
				}
				url = line[urlIdx:urlEnd]
			}

			// Use the line as snippet
			snippet = line
			if len(snippet) > 280 {
				snippet = snippet[:280] + "..."
			}
			title = truncate(snippet, 100)
		} else if strings.HasPrefix(line, "http") {
			// URL line — use as is
			url = line
			title = line
		} else {
			// Generic line
			snippet = line
			if len(snippet) > 280 {
				snippet = snippet[:280] + "..."
			}
			title = truncate(snippet, 100)
		}

		if snippet != "" || url != "" {
			results = append(results, xAIResult{
				Title:   title,
				Author:  author,
				URL:     url,
				Snippet: snippet,
			})
		}
	}

	return results
}