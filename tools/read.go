package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

type ReadTool struct{}

func (ReadTool) Name() string { return "Read" }
func (ReadTool) Description() string {
	return "Reads a local text file or small image. Text output is line-numbered; image files return base64 image content. Always Read before Edit."
}
func (ReadTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": map[string]interface{}{"type": "string"},
			"offset":    map[string]interface{}{"type": "number"},
			"limit":     map[string]interface{}{"type": "number"},
		},
		"required": []string{"file_path"},
	}
}
func (ReadTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (ReadTool) ParallelSafe() bool    { return true }

func (t ReadTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	path, ok := asString(raw["file_path"])
	if !ok || strings.TrimSpace(path) == "" {
		return nil, core.NewToolExecutionError(t.Name(), "file_path must be a non-empty string")
	}
	out := map[string]interface{}{"file_path": path}
	out["offset"] = max(1, asInt(raw["offset"], 1))
	out["limit"] = max(1, asInt(raw["limit"], 2000))
	return out, nil
}

func (t ReadTool) Summarize(input map[string]interface{}) string {
	offset := asInt(input["offset"], 1)
	limit := asInt(input["limit"], 2000)
	if offset == 1 && limit == 2000 {
		return fmt.Sprintf("Read %s", input["file_path"])
	}
	return fmt.Sprintf("Read %s (lines %d-%d)", input["file_path"], offset, offset+limit-1)
}

func (t ReadTool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	path, err := resolveFilesystem(ctx, input["file_path"].(string), core.FilesystemResolveRead)
	if err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if isDevicePath(path) {
		return core.ToolResult{Content: "Error: device path cannot be read.", Summary: t.Summarize(input), IsError: true}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	if info.IsDir() {
		return core.ToolResult{Content: "Error: path is a directory", Summary: t.Summarize(input), IsError: true}, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if media := imageMedia(ext); media != "" {
		if info.Size() > 5*1024*1024 {
			return core.ToolResult{Content: "Error: image file too large (max 5 MiB)", Summary: t.Summarize(input), IsError: true}, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
		}
		ctx.FileReadTracker.MarkRead(path)
		return core.ToolResult{
			Content: []core.ContentBlock{{Type: core.BlockImage, Source: &core.ImageSource{Type: "base64", MediaType: media, Data: base64.StdEncoding.EncodeToString(data)}}},
			Summary: fmt.Sprintf("Read image %s (%dB)", filepath.Base(path), len(data)),
		}, nil
	}
	if isKnownBinary(ext) || hasNullByte(path) {
		return core.ToolResult{Content: "Error: binary file. Use Bash to inspect.", Summary: t.Summarize(input), IsError: true}, nil
	}
	if info.Size() == 0 {
		ctx.FileReadTracker.MarkRead(path)
		return core.ToolResult{Content: "<file is empty>", Summary: t.Summarize(input)}, nil
	}
	offset := asInt(input["offset"], 1)
	limit := asInt(input["limit"], 2000)
	out, err := readLineRange(ctx.Context, path, offset, limit)
	if err != nil {
		return core.ToolResult{Content: "Error: " + err.Error(), Summary: t.Summarize(input), IsError: true}, nil
	}
	ctx.FileReadTracker.MarkRead(path)
	if out == "" {
		return core.ToolResult{Content: "<file is empty>", Summary: t.Summarize(input)}, nil
	}
	return core.ToolResult{Content: truncate(out, 30000), Summary: t.Summarize(input)}, nil
}

func imageMedia(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func isKnownBinary(ext string) bool {
	switch ext {
	case ".exe", ".so", ".dylib", ".o", ".a", ".zip", ".tar", ".gz", ".pdf":
		return true
	default:
		return false
	}
}
