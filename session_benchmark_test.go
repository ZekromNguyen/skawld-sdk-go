package skawld

import (
	"strconv"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func BenchmarkEstimateProviderTokens(b *testing.B) {
	system := []core.SystemBlock{{Type: "text", Text: "system"}}
	tools := make([]core.ToolSchema, 64)
	for i := range tools {
		tools[i] = core.ToolSchema{
			Name:        "Tool" + strconv.Itoa(i),
			Description: "benchmark tool",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}},
		}
	}
	messages := makeBenchmarkMessages(2000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = estimateProviderTokens(system, tools, messages)
	}
}

func BenchmarkEstimatedProviderTokensCached(b *testing.B) {
	agent, err := NewAgent(AgentOptions{
		Provider:          &singleTextProvider{text: "done"},
		Model:             "fake-model",
		DisableCompaction: true,
	})
	if err != nil {
		b.Fatal(err)
	}
	session := &Session{agent: agent, providerChars: estimateMessagesProviderChars(makeBenchmarkMessages(2000))}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = session.estimatedProviderTokens()
	}
}

func TestEstimatedProviderTokensCachedDoesNotAllocate(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		Provider:          &singleTextProvider{text: "done"},
		Model:             "fake-model",
		DisableCompaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{agent: agent, providerChars: estimateMessagesProviderChars(makeBenchmarkMessages(2000))}
	allocs := testing.AllocsPerRun(100, func() {
		_ = session.estimatedProviderTokens()
	})
	if allocs != 0 {
		t.Fatalf("expected cached token estimate to allocate 0 times, got %.2f", allocs)
	}
}

func makeBenchmarkMessages(count int) []core.Message {
	messages := make([]core.Message, count)
	for i := range messages {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages[i] = core.Message{Role: role, Content: []core.ContentBlock{core.Text("message " + strconv.Itoa(i) + " " + benchmarkText)}}
	}
	return messages
}

const benchmarkText = "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda"
