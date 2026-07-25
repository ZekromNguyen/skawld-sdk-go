package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type Tool struct {
	displayName string
	serverName  string
	remote      RemoteTool
	client      *Client
}

func (t *Tool) Name() string { return t.displayName }
func (t *Tool) Description() string {
	if strings.TrimSpace(t.remote.Description) != "" {
		return t.remote.Description
	}
	return fmt.Sprintf("MCP tool %s from server %s.", t.remote.Name, t.serverName)
}
func (t *Tool) InputSchema() map[string]interface{} {
	if t.remote.InputSchema == nil {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
	}
	return t.remote.InputSchema
}
func (t *Tool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (t *Tool) ParallelSafe() bool    { return false }
func (t *Tool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskHigh, SideEffect: core.SideEffectUnknown,
		Idempotency: core.IdempotencyUnsupported, Timeout: 2 * time.Minute,
		Permissions:   []string{"mcp." + t.serverName + "." + t.remote.Name},
		NetworkAccess: true, ContainsUntrusted: true,
	}
}
func (t *Tool) Validate(raw map[string]interface{}) (map[string]interface{}, error) {
	if raw == nil {
		raw = map[string]interface{}{}
	}
	for _, key := range requiredKeys(t.remote.InputSchema) {
		if _, ok := raw[key]; !ok {
			return nil, core.NewToolExecutionError(t.Name(), fmt.Sprintf("%s is required", key))
		}
	}
	if err := validateSchemaValue(raw, t.remote.InputSchema, "$"); err != nil {
		return nil, core.NewToolExecutionError(t.Name(), err.Error())
	}
	return raw, nil
}
func (t *Tool) Execute(input map[string]interface{}, ctx core.ToolContext) (core.ToolResult, error) {
	start := time.Now()
	result, err := t.client.CallTool(ctx.Context, t.remote.Name, input)
	if ctx.Observer != nil {
		ctx.Observer.Observe(ctx.Context, core.Observation{
			Type:       core.ObservationMCPCall,
			Operation:  "tool.call",
			SessionID:  ctx.SessionID,
			RunID:      ctx.RunID,
			ToolName:   t.Name(),
			DurationMS: time.Since(start).Milliseconds(),
			Error:      err,
		})
	}
	return result, err
}
func (t *Tool) Summarize(input map[string]interface{}) string {
	return fmt.Sprintf("Call MCP tool %s/%s", t.serverName, t.remote.Name)
}

func ConvertToolResult(result map[string]interface{}) core.ToolResult {
	isErr, _ := result["isError"].(bool)
	if rawContent, ok := result["content"].([]interface{}); ok {
		content, summary := convertContent(rawContent)
		return core.ToolResult{Content: content, Summary: summary, IsError: isErr}
	}
	if structured, ok := result["structuredContent"]; ok {
		raw, _ := json.Marshal(structured)
		return core.ToolResult{Content: string(raw), Summary: truncateSummary(string(raw)), IsError: isErr}
	}
	raw, _ := json.Marshal(result)
	return core.ToolResult{Content: string(raw), Summary: truncateSummary(string(raw)), IsError: isErr}
}

func convertContent(items []interface{}) (interface{}, string) {
	blocks := make([]core.ContentBlock, 0, len(items))
	hasMedia := false
	for _, item := range items {
		obj, _ := item.(map[string]interface{})
		switch obj["type"] {
		case "text":
			text, _ := obj["text"].(string)
			blocks = append(blocks, core.Text(text))
		case "image":
			hasMedia = true
			mime, _ := obj["mimeType"].(string)
			data, _ := obj["data"].(string)
			if data != "" && !isBase64(data) {
				data = base64.StdEncoding.EncodeToString([]byte(data))
			}
			blocks = append(blocks, core.ContentBlock{Type: core.BlockImage, Source: &core.ImageSource{Type: "base64", MediaType: mime, Data: data}})
		case "resource":
			hasMedia = true
			blocks = append(blocks, core.Text(renderResource(obj)))
		default:
			raw, _ := json.Marshal(obj)
			blocks = append(blocks, core.Text(string(raw)))
		}
	}
	if !hasMedia {
		var texts []string
		for _, block := range blocks {
			if block.Type == core.BlockText {
				texts = append(texts, block.Text)
			}
		}
		text := strings.Join(texts, "\n")
		return text, truncateSummary(text)
	}
	return blocks, summarizeBlocks(blocks)
}

func renderResource(obj map[string]interface{}) string {
	resource, _ := obj["resource"].(map[string]interface{})
	uri, _ := resource["uri"].(string)
	text, _ := resource["text"].(string)
	if text != "" {
		return text
	}
	if uri != "" {
		return "[resource " + uri + "]"
	}
	raw, _ := json.Marshal(obj)
	return string(raw)
}

func summarizeBlocks(blocks []core.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case core.BlockText:
			parts = append(parts, block.Text)
		case core.BlockImage:
			parts = append(parts, "[image]")
		}
	}
	return truncateSummary(strings.Join(parts, "\n"))
}

func truncateSummary(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 120 {
		return s
	}
	return s[:120] + "..."
}

func isBase64(value string) bool {
	if value == "" {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(value)
	return err == nil
}

func requiredKeys(schema map[string]interface{}) []string {
	if schema == nil {
		return nil
	}
	switch vals := schema["required"].(type) {
	case []string:
		return append([]string(nil), vals...)
	case []interface{}:
		out := make([]string, 0, len(vals))
		for _, val := range vals {
			if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func validateSchemaValue(value interface{}, schema map[string]interface{}, path string) error {
	if schema == nil {
		return nil
	}
	if allowed, ok := schema["enum"].([]interface{}); ok {
		matched := false
		for _, candidate := range allowed {
			if reflect.DeepEqual(value, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be one of the declared enum values", path)
		}
	}
	schemaType, _ := schema["type"].(string)
	switch schemaType {
	case "":
		return nil
	case "object":
		object, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := schema["properties"].(map[string]interface{})
		for name, child := range object {
			childSchema, declared := properties[name].(map[string]interface{})
			if !declared {
				if allow, ok := schema["additionalProperties"].(bool); ok && !allow {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
				continue
			}
			if err := validateSchemaValue(child, childSchema, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		itemSchema, _ := schema["items"].(map[string]interface{})
		for index, item := range items {
			if err := validateSchemaValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "number":
		if !isJSONNumber(value, false) {
			return fmt.Errorf("%s must be a number", path)
		}
	case "integer":
		if !isJSONNumber(value, true) {
			return fmt.Errorf("%s must be an integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
	default:
		return fmt.Errorf("%s uses unsupported JSON schema type %q", path, schemaType)
	}
	return nil
}

func isJSONNumber(value interface{}, integer bool) bool {
	switch number := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return !integer || number == float32(int64(number))
	case float64:
		return !integer || number == float64(int64(number))
	case json.Number:
		if integer {
			_, err := number.Int64()
			return err == nil
		}
		_, err := number.Float64()
		return err == nil
	default:
		return false
	}
}
