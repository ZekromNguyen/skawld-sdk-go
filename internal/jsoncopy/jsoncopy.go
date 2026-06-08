package jsoncopy

import "github.com/skawld/skawld-sdk-go/core"

// Map deep-copies a JSON-like object map.
func Map(in map[string]interface{}) map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = Value(v)
	}
	return out
}

// MapSlice deep-copies a slice of JSON-like object maps.
func MapSlice(in []map[string]interface{}) []map[string]interface{} {
	if in == nil {
		return nil
	}
	out := make([]map[string]interface{}, len(in))
	for i, item := range in {
		out[i] = Map(item)
	}
	return out
}

// Value deep-copies JSON-like values and SDK content-block values that carry
// JSON maps.
func Value(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return Map(v)
	case []map[string]interface{}:
		return MapSlice(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			out[i] = Value(v[i])
		}
		return out
	case []string:
		return append([]string(nil), v...)
	case []core.ContentBlock:
		return ContentBlocks(v)
	case core.ContentBlock:
		return ContentBlock(v)
	default:
		return v
	}
}

// SessionRecord deep-copies mutable session record fields.
func SessionRecord(rec core.SessionRecord) core.SessionRecord {
	rec.Meta = Map(rec.Meta)
	rec.InvokedSkills = append([]core.InvokedSkillRecord(nil), rec.InvokedSkills...)
	return rec
}

// StoredMessages deep-copies stored messages.
func StoredMessages(messages []core.StoredMessage) []core.StoredMessage {
	out := make([]core.StoredMessage, len(messages))
	for i, msg := range messages {
		out[i] = msg
		out[i].Message = Message(msg.Message)
	}
	return out
}

// Message deep-copies mutable message fields.
func Message(msg core.Message) core.Message {
	msg.Content = ContentBlocks(msg.Content)
	msg.ProviderMetadata = ProviderMetadata(msg.ProviderMetadata)
	return msg
}

// ProviderMetadata deep-copies provider replay metadata.
func ProviderMetadata(meta core.MessageProviderMetadata) core.MessageProviderMetadata {
	if meta.OpenAIResponses == nil {
		return meta
	}
	responses := *meta.OpenAIResponses
	responses.OutputItems = MapSlice(responses.OutputItems)
	meta.OpenAIResponses = &responses
	return meta
}

// ContentBlocks deep-copies content blocks.
func ContentBlocks(blocks []core.ContentBlock) []core.ContentBlock {
	out := make([]core.ContentBlock, len(blocks))
	for i, block := range blocks {
		out[i] = ContentBlock(block)
	}
	return out
}

// ContentBlock deep-copies mutable content block fields.
func ContentBlock(block core.ContentBlock) core.ContentBlock {
	block.Input = Map(block.Input)
	block.Content = Value(block.Content)
	if nested, ok := block.Content.([]core.ContentBlock); ok {
		block.Content = ContentBlocks(nested)
	}
	if nested, ok := block.Content.(core.ContentBlock); ok {
		block.Content = ContentBlock(nested)
	}
	if block.Source != nil {
		source := *block.Source
		block.Source = &source
	}
	return block
}

// Task deep-copies mutable task fields.
func Task(task core.Task) core.Task {
	task.Blocks = append([]string(nil), task.Blocks...)
	task.BlockedBy = append([]string(nil), task.BlockedBy...)
	task.Metadata = Map(task.Metadata)
	return task
}
