package providers

import (
	"strconv"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func BenchmarkOpenAIChatTranslateMessages(b *testing.B) {
	provider := NewOpenAIChatCompletionsProvider(OpenAIOptions{})
	req := core.ProviderRequest{
		System:   []core.SystemBlock{{Type: "text", Text: "system"}, {Type: "text", Text: "rules"}},
		Tools:    []core.ToolSchema{{Name: "Read", Description: "read", InputSchema: map[string]interface{}{"type": "object"}}},
		Messages: makeProviderBenchmarkMessages(1000),
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = provider.translateMessages(req)
	}
}

func BenchmarkOpenAIResponsesInput(b *testing.B) {
	messages := makeProviderBenchmarkMessages(1000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = responsesInputAndPrevious(messages)
	}
}

func makeProviderBenchmarkMessages(count int) []core.Message {
	messages := make([]core.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, core.Message{Role: "user", Content: []core.ContentBlock{core.Text("prompt " + strconv.Itoa(i))}})
		messages = append(messages, core.Message{
			Role: "assistant",
			Content: []core.ContentBlock{
				core.Text("answer " + strconv.Itoa(i)),
				core.ToolUse("call_"+strconv.Itoa(i), "Read", map[string]interface{}{"file_path": "file" + strconv.Itoa(i) + ".go"}),
			},
		})
		messages = append(messages, core.Message{Role: "user", Content: []core.ContentBlock{core.ToolResultBlock("call_"+strconv.Itoa(i), "ok", false)}})
	}
	return messages
}
