package tools

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// ---------------------------------------------------------------------------
// VisionAnalyze tests
// ---------------------------------------------------------------------------

func TestVisionAnalyzeValidate(t *testing.T) {
	tool := VisionAnalyzeTool{}

	input, err := tool.Validate(map[string]interface{}{
		"image_path": "/tmp/test.png",
		"prompt":     "What do you see?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["image_path"] != "/tmp/test.png" {
		t.Fatalf("unexpected image_path: %v", input["image_path"])
	}
	if input["prompt"] != "What do you see?" {
		t.Fatalf("unexpected prompt: %v", input["prompt"])
	}

	// Test missing image_path
	_, err = tool.Validate(map[string]interface{}{
		"prompt": "test",
	})
	if err == nil {
		t.Fatal("expected error for missing image_path")
	}

	// Test empty image_path
	_, err = tool.Validate(map[string]interface{}{
		"image_path": "",
	})
	if err == nil {
		t.Fatal("expected error for empty image_path")
	}

	// Test optional prompt
	input, err = tool.Validate(map[string]interface{}{
		"image_path": "/tmp/test.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, hasPrompt := input["prompt"]; hasPrompt {
		t.Fatal("prompt should not be present when not provided")
	}
}

func TestVisionAnalyzeSummarize(t *testing.T) {
	tool := VisionAnalyzeTool{}
	summary := tool.Summarize(map[string]interface{}{"image_path": "/tmp/cat.png"})
	if !strings.Contains(summary, "cat.png") {
		t.Fatalf("unexpected summary: %s", summary)
	}

	summaryWithPrompt := tool.Summarize(map[string]interface{}{"image_path": "/tmp/cat.png", "prompt": "Describe this cat"})
	if !strings.Contains(summaryWithPrompt, "Describe this cat") {
		t.Fatalf("unexpected summary with prompt: %s", summaryWithPrompt)
	}
}

func TestVisionAnalyzeParallelSafe(t *testing.T) {
	tool := VisionAnalyzeTool{}
	if !tool.ParallelSafe() {
		t.Error("VisionAnalyze should be parallel safe")
	}
}

func TestVisionAnalyzeScope(t *testing.T) {
	tool := VisionAnalyzeTool{}
	if tool.Scope() != core.ToolScopeRead {
		t.Error("VisionAnalyze should be read-only scope")
	}
}

func TestVisionAnalyzeMissingFile(t *testing.T) {
	tool := VisionAnalyzeTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"image_path": "/nonexistent/file/that/does/not/exist.png",
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing file")
	}
}

func TestVisionAnalyzeUnsupportedFormat(t *testing.T) {
	// Create a temp file with an unsupported extension
	tmpFile, err := os.CreateTemp("", "skawld-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("not an image")

	tool := VisionAnalyzeTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"image_path": tmpFile.Name(),
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for unsupported format")
	}
	content := fmt.Sprint(res.Content)
	if !strings.Contains(content, "unsupported image format") {
		t.Fatalf("expected unsupported format message, got: %s", content)
	}
}

func TestVisionAnalyzeValidImage(t *testing.T) {
	// Create a real PNG image
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createTestPNG(t, imgPath)

	tool := VisionAnalyzeTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"image_path": imgPath,
		"prompt":     "Analyze this",
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %v", res.Content)
	}

	// Check that we get content blocks
	blocks, ok := res.Content.([]core.ContentBlock)
	if !ok {
		t.Fatalf("expected []core.ContentBlock, got %T", res.Content)
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 content blocks, got %d", len(blocks))
	}
	if blocks[0].Type != core.BlockText {
		t.Errorf("first block should be text, got %v", blocks[0].Type)
	}
	if blocks[1].Type != core.BlockImage {
		t.Errorf("second block should be image, got %v", blocks[1].Type)
	}
	if blocks[1].Source == nil {
		t.Fatal("image block should have Source")
	}
	if blocks[1].Source.MediaType != "image/png" {
		t.Errorf("expected image/png, got %s", blocks[1].Source.MediaType)
	}
	if len(blocks[1].Source.Data) == 0 {
		t.Error("image data should not be empty")
	}
}

func TestImageMediaForPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"test.png", "image/png"},
		{"test.PNG", "image/png"},
		{"test.jpg", "image/jpeg"},
		{"test.jpeg", "image/jpeg"},
		{"test.JPEG", "image/jpeg"},
		{"test.gif", "image/gif"},
		{"test.webp", "image/webp"},
		{"test.txt", ""},
		{"test", ""},
	}
	for _, tt := range tests {
		got := imageMediaForPath(tt.path)
		if got != tt.expected {
			t.Errorf("imageMediaForPath(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// ImageGenerate tests
// ---------------------------------------------------------------------------

func TestImageGenerateValidate(t *testing.T) {
	tool := ImageGenerateTool{}

	input, err := tool.Validate(map[string]interface{}{
		"prompt": "A cat in space",
		"size":   "512x512",
		"n":      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["prompt"] != "A cat in space" {
		t.Fatalf("unexpected prompt: %v", input["prompt"])
	}
	if input["size"] != "512x512" {
		t.Fatalf("unexpected size: %v", input["size"])
	}
	if input["n"] != 2 {
		t.Fatalf("unexpected n: %v", input["n"])
	}

	// Test missing prompt
	_, err = tool.Validate(map[string]interface{}{"size": "512x512"})
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}

	// Test default size
	input, err = tool.Validate(map[string]interface{}{
		"prompt": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["size"] != "1024x1024" {
		t.Fatalf("expected default size 1024x1024, got %v", input["size"])
	}
	if input["n"] != 1 {
		t.Fatalf("expected default n=1, got %v", input["n"])
	}

	// Test n clamping
	input, err = tool.Validate(map[string]interface{}{
		"prompt": "test",
		"n":      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["n"] != 4 {
		t.Fatalf("expected n clamped to 4, got %v", input["n"])
	}

	input, err = tool.Validate(map[string]interface{}{
		"prompt": "test",
		"n":      0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["n"] != 1 {
		t.Fatalf("expected n clamped to 1, got %v", input["n"])
	}
}

func TestImageGenerateMissingAPIKey(t *testing.T) {
	oldKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer func() {
		if oldKey != "" {
			os.Setenv("OPENAI_API_KEY", oldKey)
		}
	}()

	tool := ImageGenerateTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"prompt": "test",
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when OPENAI_API_KEY is not set")
	}
	content := fmt.Sprint(res.Content)
	if !strings.Contains(content, "OPENAI_API_KEY") {
		t.Fatalf("expected OPENAI_API_KEY mention: %s", content)
	}
}

func TestImageGenerateParallelSafe(t *testing.T) {
	tool := ImageGenerateTool{}
	if !tool.ParallelSafe() {
		t.Error("ImageGenerate should be parallel safe")
	}
}

func TestImageGenerateScope(t *testing.T) {
	tool := ImageGenerateTool{}
	if tool.Scope() != core.ToolScopeWrite {
		t.Error("ImageGenerate should be write scope")
	}
}

func TestImageGenerateSummarize(t *testing.T) {
	tool := ImageGenerateTool{}
	summary := tool.Summarize(map[string]interface{}{
		"prompt": "A cat in space",
		"size":   "512x512",
	})
	if !strings.Contains(summary, "A cat in space") {
		t.Fatalf("unexpected summary: %s", summary)
	}
	if !strings.Contains(summary, "512x512") {
		t.Fatalf("summary should contain size: %s", summary)
	}
}

// ---------------------------------------------------------------------------
// TextToSpeech tests
// ---------------------------------------------------------------------------

func TestTTSValidate(t *testing.T) {
	tool := TextToSpeechTool{}

	input, err := tool.Validate(map[string]interface{}{
		"text":  "Hello world",
		"voice": "nova",
		"speed": 1.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["text"] != "Hello world" {
		t.Fatalf("unexpected text: %v", input["text"])
	}
	if input["voice"] != "nova" {
		t.Fatalf("unexpected voice: %v", input["voice"])
	}
	if input["speed"] != 1.5 {
		t.Fatalf("unexpected speed: %v", input["speed"])
	}

	// Test missing text
	_, err = tool.Validate(map[string]interface{}{"voice": "alloy"})
	if err == nil {
		t.Fatal("expected error for missing text")
	}

	// Test defaults
	input, err = tool.Validate(map[string]interface{}{
		"text": "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["voice"] != "alloy" {
		t.Fatalf("expected default voice alloy, got %v", input["voice"])
	}
	if input["speed"] != 1.0 {
		t.Fatalf("expected default speed 1.0, got %v", input["speed"])
	}

	// Test speed clamping
	input, err = tool.Validate(map[string]interface{}{
		"text":  "test",
		"speed": 10.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["speed"] != 4.0 {
		t.Fatalf("expected speed clamped to 4.0, got %v", input["speed"])
	}

	input, err = tool.Validate(map[string]interface{}{
		"text":  "test",
		"speed": 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["speed"] != 0.25 {
		t.Fatalf("expected speed clamped to 0.25, got %v", input["speed"])
	}

	// Test speed from int
	input, err = tool.Validate(map[string]interface{}{
		"text":  "test",
		"speed": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["speed"] != 2.0 {
		t.Fatalf("expected speed 2.0 from int, got %v", input["speed"])
	}
}

func TestTTSMissingAPIKey(t *testing.T) {
	oldKey := os.Getenv("OPENAI_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	defer func() {
		if oldKey != "" {
			os.Setenv("OPENAI_API_KEY", oldKey)
		}
	}()

	tool := TextToSpeechTool{}
	input, _ := tool.Validate(map[string]interface{}{
		"text": "Hello",
	})
	res, err := tool.Execute(input, core.ToolContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when OPENAI_API_KEY is not set")
	}
	content := fmt.Sprint(res.Content)
	if !strings.Contains(content, "OPENAI_API_KEY") {
		t.Fatalf("expected OPENAI_API_KEY mention: %s", content)
	}
}

func TestTTSParallelSafe(t *testing.T) {
	tool := TextToSpeechTool{}
	if !tool.ParallelSafe() {
		t.Error("TextToSpeech should be parallel safe")
	}
}

func TestTTSScope(t *testing.T) {
	tool := TextToSpeechTool{}
	if tool.Scope() != core.ToolScopeWrite {
		t.Error("TextToSpeech should be write scope")
	}
}

func TestTTSSummarize(t *testing.T) {
	tool := TextToSpeechTool{}
	summary := tool.Summarize(map[string]interface{}{
		"text":  "Hello world",
		"voice": "alloy",
	})
	if !strings.Contains(summary, "Hello world") {
		t.Fatalf("unexpected summary: %s", summary)
	}
	if !strings.Contains(summary, "alloy") {
		t.Fatalf("summary should contain voice: %s", summary)
	}
}

func TestTTSSpeedFromFloat32(t *testing.T) {
	tool := TextToSpeechTool{}
	input, err := tool.Validate(map[string]interface{}{
		"text":  "test",
		"speed": float32(1.25),
	})
	if err != nil {
		t.Fatal(err)
	}
	if input["speed"] != 1.25 {
		t.Fatalf("expected speed 1.25 from float32, got %v", input["speed"])
	}
}

// ---------------------------------------------------------------------------
// Test input types
// ---------------------------------------------------------------------------

func TestImageGenerateInputFrom(t *testing.T) {
	in := imageGenerateInputFrom(map[string]interface{}{
		"prompt": "test",
		"size":   "512x512",
		"n":      2,
	})
	if in.Prompt != "test" || in.Size != "512x512" || in.N != 2 {
		t.Fatalf("unexpected parsed input: %+v", in)
	}
}

func TestVisionAnalyzeInputFrom(t *testing.T) {
	in := visionAnalyzeInputFrom(map[string]interface{}{
		"image_path": "/tmp/img.png",
		"prompt":     "analyze",
	})
	if in.ImagePath != "/tmp/img.png" || in.Prompt != "analyze" {
		t.Fatalf("unexpected parsed input: %+v", in)
	}
}

func TestTTSInputFrom(t *testing.T) {
	in := ttsInputFrom(map[string]interface{}{
		"text":  "hello",
		"voice": "fable",
		"speed": 2.0,
	})
	if in.Text != "hello" || in.Voice != "fable" || in.Speed != 2.0 {
		t.Fatalf("unexpected parsed input: %+v", in)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func createTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{0, 255, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{255, 255, 255, 255})

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
