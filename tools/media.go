package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
)

// ---------------------------------------------------------------------------
// Vision Analyze tool — reads an image, encodes as base64, returns a vision
// content block the model can process.
// ---------------------------------------------------------------------------

// VisionAnalyzeTool reads an image from disk and encodes it into a content
// block suitable for vision-capable models. Supports PNG, JPEG, GIF, WebP.
type VisionAnalyzeTool struct{}

func (t VisionAnalyzeTool) Name() string { return "VisionAnalyze" }
func (t VisionAnalyzeTool) Description() string {
	return "Read and analyze an image file. Provide an optional prompt to guide analysis."
}
func (t VisionAnalyzeTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (t VisionAnalyzeTool) ParallelSafe() bool    { return true }

func (t VisionAnalyzeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"image_path": map[string]interface{}{
				"type":        "string",
				"description": "Absolute path to an image file (PNG, JPEG, GIF, WebP).",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Optional natural-language prompt to guide analysis of the image.",
			},
		},
		"required": []string{"image_path"},
	}
}

func (t VisionAnalyzeTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	in, err := parseVisionAnalyzeInput(raw)
	if err != nil {
		return nil, err
	}
	return in.mapValue(), nil
}

func (t VisionAnalyzeTool) Execute(validated map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := visionAnalyzeInputFrom(validated)

	data, err := os.ReadFile(in.ImagePath)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("cannot read %q: %v", in.ImagePath, err)}, nil
	}
	mediaType := imageMediaForPath(in.ImagePath)
	if mediaType == "" {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("unsupported image format: %s", filepath.Ext(in.ImagePath))}, nil
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	blocks := []core.ContentBlock{
		{
			Type:   core.BlockText,
			Text:   "Image loaded: " + filepath.Base(in.ImagePath),
			Source: nil,
		},
		{
			Type: core.BlockImage,
			Source: &core.ImageSource{
				Type:      "base64",
				MediaType: mediaType,
				Data:      b64,
			},
		},
	}
	if in.Prompt != "" {
		blocks = append(blocks, core.ContentBlock{
			Type:   core.BlockText,
			Text:   "Prompt: " + in.Prompt,
			Source: nil,
		})
	}
	return core.ToolResult{Content: blocks}, nil
}

func (t VisionAnalyzeTool) Summarize(validated map[string]interface{}) string {
	in := visionAnalyzeInputFrom(validated)
	suffix := ""
	if in.Prompt != "" {
		suffix = " — " + in.Prompt
	}
	return "Analyze " + filepath.Base(in.ImagePath) + suffix
}

// imageMediaForPath returns a MIME type for the file extension; empty string
// if unsupported.
func imageMediaForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
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

// ---------------------------------------------------------------------------
// Image Generate tool — calls the DALL·E API.
// ---------------------------------------------------------------------------

// ImageGenerateTool generates images via the OpenAI DALL·E API.
type ImageGenerateTool struct {
	httpClient *http.Client
}

func (t ImageGenerateTool) Name() string { return "ImageGenerate" }
func (t ImageGenerateTool) Description() string {
	return "Generate an image from a text prompt using DALL·E."
}
func (t ImageGenerateTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t ImageGenerateTool) ParallelSafe() bool    { return true }

func (t ImageGenerateTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Text description of the image to generate.",
			},
			"size": map[string]interface{}{
				"type":        "string",
				"description": "Image size: 256x256, 512x512, or 1024x1024 (default).",
				"default":     "1024x1024",
			},
			"n": map[string]interface{}{
				"type":        "integer",
				"description": "Number of images to generate (1-4, default 1).",
				"default":     1,
			},
		},
		"required": []string{"prompt"},
	}
}

func (t ImageGenerateTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	in, err := parseImageGenerateInput(raw)
	if err != nil {
		return nil, err
	}
	return in.mapValue(), nil
}

func (t ImageGenerateTool) Execute(validated map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := imageGenerateInputFrom(validated)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return core.ToolResult{IsError: true, Content: "OPENAI_API_KEY is not set"}, nil
	}

	body := map[string]interface{}{
		"model":           "dall-e-3",
		"prompt":          in.Prompt,
		"n":               in.N,
		"size":            in.Size,
		"response_format": "b64_json",
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("marshal request: %v", err)}, nil
	}

	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx.Context, "POST", "https://api.openai.com/v1/images/generations", bytes.NewReader(bodyBytes))
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("create request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("API call failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("read response: %v", err)}, nil
	}

	if resp.StatusCode >= 400 {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("DALL·E API returned %d: %s", resp.StatusCode, string(respBody))}, nil
	}

	var result dalleResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("parse response: %v", err)}, nil
	}

	blocks := []core.ContentBlock{
		{Type: core.BlockText, Text: fmt.Sprintf("Generated %d image(s) for: %s", len(result.Data), in.Prompt)},
	}
	for _, d := range result.Data {
		blocks = append(blocks, core.ContentBlock{
			Type: core.BlockImage,
			Source: &core.ImageSource{
				Type:      "base64",
				MediaType: "image/png",
				Data:      d.B64JSON,
			},
		})
	}
	return core.ToolResult{Content: blocks}, nil
}

type dalleResponse struct {
	Data []dalleImageData `json:"data"`
}

type dalleImageData struct {
	B64JSON       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}

func (t ImageGenerateTool) Summarize(validated map[string]interface{}) string {
	in := imageGenerateInputFrom(validated)
	return "Generate " + in.Size + " image: " + truncate(in.Prompt, 60)
}

// ---------------------------------------------------------------------------
// Text-to-Speech tool — calls the OpenAI TTS API.
// ---------------------------------------------------------------------------

// TextToSpeechTool converts text to spoken audio via the OpenAI TTS API.
type TextToSpeechTool struct {
	httpClient *http.Client
}

func (t TextToSpeechTool) Name() string { return "TextToSpeech" }
func (t TextToSpeechTool) Description() string {
	return "Convert text to speech using OpenAI TTS and save the audio file."
}
func (t TextToSpeechTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t TextToSpeechTool) ParallelSafe() bool    { return true }

func (t TextToSpeechTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "The text to convert to speech (max 4096 characters).",
			},
			"voice": map[string]interface{}{
				"type":        "string",
				"description": "Voice to use: alloy, echo, fable, onyx, nova, or shimmer (default: alloy).",
				"default":     "alloy",
			},
			"speed": map[string]interface{}{
				"type":        "number",
				"description": "Playback speed (0.25 to 4.0, default 1.0).",
				"default":     1.0,
			},
		},
		"required": []string{"text"},
	}
}

func (t TextToSpeechTool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	in, err := parseTTSInput(raw)
	if err != nil {
		return nil, err
	}
	return in.mapValue(), nil
}

func (t TextToSpeechTool) Execute(validated map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	in := ttsInputFrom(validated)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return core.ToolResult{IsError: true, Content: "OPENAI_API_KEY is not set"}, nil
	}

	body := map[string]interface{}{
		"model": "tts-1",
		"input": in.Text,
		"voice": in.Voice,
		"speed": in.Speed,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("marshal request: %v", err)}, nil
	}

	client := t.httpClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx.Context, "POST", "https://api.openai.com/v1/audio/speech", bytes.NewReader(bodyBytes))
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("create request: %v", err)}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("API call failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("read response: %v", err)}, nil
	}

	if resp.StatusCode >= 400 {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("TTS API returned %d: %s", resp.StatusCode, string(audioData))}, nil
	}

	// Write audio to a temp file so the caller can reference it.
	tmpFile, err := os.CreateTemp("", "skawld-tts-*.mp3")
	if err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("create temp file: %v", err)}, nil
	}
	if _, err := tmpFile.Write(audioData); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("write audio: %v", err)}, nil
	}
	if err := tmpFile.Close(); err != nil {
		return core.ToolResult{IsError: true, Content: fmt.Sprintf("close temp file: %v", err)}, nil
	}

	dur := fmt.Sprintf("%.1fs", float64(len(audioData))/192000.0) // rough estimate of MP3 duration
	return core.ToolResult{
		Content: fmt.Sprintf("Audio saved to %s (~%s, voice=%s, speed=%.2fx): \"%s\"",
			tmpFile.Name(), dur, in.Voice, in.Speed, truncate(in.Text, 80)),
	}, nil
}

func (t TextToSpeechTool) Summarize(validated map[string]interface{}) string {
	in := ttsInputFrom(validated)
	return "Speak \"" + truncate(in.Text, 50) + "\" (" + in.Voice + ")"
}
