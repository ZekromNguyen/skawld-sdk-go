package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/chromedp"
	"github.com/skawld/skawld-sdk-go/core"
)

type BrowserNavigateTool struct{}
type BrowserSnapshotTool struct{}
type BrowserVisionTool struct{}

var browserState = struct {
	sync.Mutex
	allocCtx  context.Context
	cancel    context.CancelFunc
	ctx       context.Context
	ctxCancel context.CancelFunc
	current   string
}{}

func (BrowserNavigateTool) Name() string { return "BrowserNavigate" }
func (BrowserNavigateTool) Description() string {
	return "Navigate the shared headless browser to a URL."
}
func (BrowserNavigateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":        map[string]interface{}{"type": "string", "description": "URL to open."},
			"timeout_ms": map[string]interface{}{"type": "number", "description": "Navigation timeout, default 30000ms."},
		},
		"required": []string{"url"},
	}
}
func (BrowserNavigateTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (BrowserNavigateTool) ParallelSafe() bool    { return false }
func (t BrowserNavigateTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseBrowserNavigateInput(raw)
	if err != nil {
		return nil, err
	}
	if _, err := url.ParseRequestURI(parsed.URL); err != nil {
		return nil, core.NewToolExecutionError(t.Name(), "url must be an absolute URL")
	}
	return parsed.mapValue(), nil
}
func (t BrowserNavigateTool) Summarize(input map[string]interface{}) string {
	return "Navigate browser to " + truncate(input["url"].(string), 80)
}
func (t BrowserNavigateTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := browserNavigateInputFrom(input)
	browserState.Lock()
	defer browserState.Unlock()
	pageCtx, err := ensureBrowser(ctx.Context)
	if err != nil {
		return browserToolError(t, input, err), nil
	}
	runCtx, cancel := context.WithTimeout(pageCtx, time.Duration(in.TimeoutMS)*time.Millisecond)
	defer cancel()
	if err := chromedp.Run(runCtx, chromedp.Navigate(in.URL), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return browserToolError(t, input, err), nil
	}
	browserState.current = in.URL
	return core.ToolResult{Content: "Browser navigated to " + in.URL, Summary: t.Summarize(input)}, nil
}

func (BrowserSnapshotTool) Name() string { return "BrowserSnapshot" }
func (BrowserSnapshotTool) Description() string {
	return "Capture the current browser page accessibility tree as text."
}
func (BrowserSnapshotTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"depth": map[string]interface{}{"type": "number", "description": "Maximum accessibility tree depth, default 6."},
		},
	}
}
func (BrowserSnapshotTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (BrowserSnapshotTool) ParallelSafe() bool    { return false }
func (t BrowserSnapshotTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseBrowserSnapshotInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t BrowserSnapshotTool) Summarize(input map[string]interface{}) string {
	return "Capture browser accessibility snapshot"
}
func (t BrowserSnapshotTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := browserSnapshotInputFrom(input)
	browserState.Lock()
	defer browserState.Unlock()
	pageCtx, err := ensureBrowser(ctx.Context)
	if err != nil {
		return browserToolError(t, input, err), nil
	}
	var nodes []*accessibility.Node
	if err := chromedp.Run(pageCtx, chromedp.ActionFunc(func(c context.Context) error {
		var err error
		nodes, err = accessibility.GetFullAXTree().WithDepth(int64(in.Depth)).Do(c)
		return err
	})); err != nil {
		return browserToolError(t, input, err), nil
	}
	out := renderAXTree(nodes)
	if out == "" {
		out = "<empty accessibility tree>"
	}
	return core.ToolResult{Content: truncate(out, 30000), Summary: fmt.Sprintf("Browser snapshot: %d node(s)", len(nodes))}, nil
}

func (BrowserVisionTool) Name() string { return "BrowserVision" }
func (BrowserVisionTool) Description() string {
	return "Take a PNG screenshot of the current browser page and return it as image content."
}
func (BrowserVisionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"full_page": map[string]interface{}{"type": "boolean", "description": "Capture full page instead of viewport."},
			"quality":   map[string]interface{}{"type": "number", "description": "Screenshot quality hint, default 90."},
		},
	}
}
func (BrowserVisionTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (BrowserVisionTool) ParallelSafe() bool    { return false }
func (t BrowserVisionTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	parsed, err := parseBrowserVisionInput(raw)
	if err != nil {
		return nil, err
	}
	return parsed.mapValue(), nil
}
func (t BrowserVisionTool) Summarize(input map[string]interface{}) string {
	return "Capture browser screenshot"
}
func (t BrowserVisionTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := browserVisionInputFrom(input)
	browserState.Lock()
	defer browserState.Unlock()
	pageCtx, err := ensureBrowser(ctx.Context)
	if err != nil {
		return browserToolError(t, input, err), nil
	}
	var data []byte
	if in.FullPage {
		err = chromedp.Run(pageCtx, chromedp.FullScreenshot(&data, in.Quality))
	} else {
		err = chromedp.Run(pageCtx, chromedp.CaptureScreenshot(&data))
	}
	if err != nil {
		return browserToolError(t, input, err), nil
	}
	return core.ToolResult{
		Content: []core.ContentBlock{{Type: core.BlockImage, Source: &core.ImageSource{Type: "base64", MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(data)}}},
		Summary: fmt.Sprintf("Browser screenshot: %dB", len(data)),
	}, nil
}

func ensureBrowser(parent context.Context) (context.Context, error) {
	if parent == nil {
		parent = context.Background()
	}
	if browserState.ctx != nil {
		select {
		case <-browserState.ctx.Done():
			browserState.ctx = nil
			if browserState.ctxCancel != nil {
				browserState.ctxCancel()
			}
			if browserState.cancel != nil {
				browserState.cancel()
			}
		default:
			return browserState.ctx, nil
		}
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1280, 900),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(parent, opts...)
	pageCtx, pageCancel := chromedp.NewContext(allocCtx)
	startCtx, startCancel := context.WithTimeout(pageCtx, 20*time.Second)
	defer startCancel()
	if err := chromedp.Run(startCtx); err != nil {
		pageCancel()
		cancel()
		return nil, err
	}
	browserState.allocCtx = allocCtx
	browserState.cancel = cancel
	browserState.ctx = pageCtx
	browserState.ctxCancel = pageCancel
	return pageCtx, nil
}

func browserToolError(tool interface {
	Summarize(map[string]interface{}) string
}, input map[string]interface{}, err error) core.ToolResult {
	return core.ToolResult{Content: "Browser error: " + err.Error(), Summary: tool.Summarize(input), IsError: true}
}

func renderAXTree(nodes []*accessibility.Node) string {
	if len(nodes) == 0 {
		return ""
	}
	byID := make(map[accessibility.NodeID]*accessibility.Node, len(nodes))
	children := make(map[accessibility.NodeID][]*accessibility.Node)
	for _, node := range nodes {
		byID[node.NodeID] = node
		if node.ParentID != "" {
			children[node.ParentID] = append(children[node.ParentID], node)
		}
	}
	roots := make([]*accessibility.Node, 0, 1)
	for _, node := range nodes {
		if node.ParentID == "" {
			roots = append(roots, node)
			continue
		}
		if _, ok := byID[node.ParentID]; !ok {
			roots = append(roots, node)
		}
	}
	if len(roots) == 0 {
		roots = append(roots, nodes[0])
	}
	var b strings.Builder
	seen := make(map[accessibility.NodeID]bool)
	for _, root := range roots {
		renderAXNode(&b, root, children, seen, 0)
	}
	return strings.TrimSpace(b.String())
}

func renderAXNode(b *strings.Builder, node *accessibility.Node, children map[accessibility.NodeID][]*accessibility.Node, seen map[accessibility.NodeID]bool, depth int) {
	if node == nil || seen[node.NodeID] || node.Ignored {
		return
	}
	seen[node.NodeID] = true
	role := axValueString(node.Role)
	name := axValueString(node.Name)
	value := axValueString(node.Value)
	description := axValueString(node.Description)
	if role == "" && name == "" && value == "" && description == "" {
		for _, child := range children[node.NodeID] {
			renderAXNode(b, child, children, seen, depth)
		}
		return
	}
	b.WriteString(strings.Repeat("  ", depth))
	if role != "" {
		b.WriteString(role)
	} else {
		b.WriteString("node")
	}
	if name != "" {
		b.WriteString(" \"")
		b.WriteString(name)
		b.WriteString("\"")
	}
	if value != "" && value != name {
		b.WriteString(" value=\"")
		b.WriteString(value)
		b.WriteString("\"")
	}
	if description != "" {
		b.WriteString(" description=\"")
		b.WriteString(description)
		b.WriteString("\"")
	}
	b.WriteByte('\n')
	for _, child := range children[node.NodeID] {
		renderAXNode(b, child, children, seen, depth+1)
	}
}

func axValueString(value *accessibility.Value) string {
	if value == nil || len(value.Value) == 0 {
		return ""
	}
	var decoded interface{}
	if err := json.Unmarshal(value.Value, &decoded); err != nil {
		return strings.Trim(string(value.Value), `"`)
	}
	switch v := decoded.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	default:
		return strings.Trim(string(value.Value), `"`)
	}
}
