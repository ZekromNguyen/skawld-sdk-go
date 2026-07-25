package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestBrowserToolsValidateAndSummarize(t *testing.T) {
	nav := &BrowserNavigateTool{}
	input, err := nav.Validate(map[string]interface{}{"url": "https://example.com", "timeout_ms": 500})
	if err != nil {
		t.Fatal(err)
	}
	if input["timeout_ms"] != 1000 {
		t.Fatalf("expected timeout clamp, got %+v", input)
	}
	if !strings.Contains(nav.Summarize(input), "example.com") {
		t.Fatalf("unexpected summary: %s", nav.Summarize(input))
	}
	if _, err := nav.Validate(map[string]interface{}{"url": "not a url"}); err == nil {
		t.Fatal("expected invalid URL to fail")
	}
}

func TestBrowserNavigateSnapshotAndVision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	toolCtx := core.ToolContext{Context: ctx}

	nav := &BrowserNavigateTool{}
	navInput, err := nav.Validate(map[string]interface{}{
		"url": "data:text/html,<html><body><main><h1>Browser Test</h1><button>Click Me</button></main></body></html>",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := nav.Execute(navInput, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Skipf("headless browser unavailable: %v", res.Content)
	}

	snapshot := &BrowserSnapshotTool{}
	snapshotInput, err := snapshot.Validate(map[string]interface{}{"depth": 8})
	if err != nil {
		t.Fatal(err)
	}
	res, err = snapshot.Execute(snapshotInput, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(fmt.Sprint(res.Content), "Browser Test") {
		t.Fatalf("unexpected snapshot result: %+v", res)
	}

	vision := &BrowserVisionTool{}
	visionInput, err := vision.Validate(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	res, err = vision.Execute(visionInput, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected screenshot error: %+v", res)
	}
	blocks, ok := res.Content.([]core.ContentBlock)
	if !ok || len(blocks) != 1 || blocks[0].Source == nil || blocks[0].Source.MediaType != "image/png" || blocks[0].Source.Data == "" {
		t.Fatalf("unexpected screenshot content: %+v", res.Content)
	}
}
