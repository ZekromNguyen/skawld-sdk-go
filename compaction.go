package skawld

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skawld/skawld-sdk-go/core"
)

const (
	compactionTriggerProactive = "proactive"
	compactionTriggerForced    = "forced"

	defaultCompactionTurns     = 10
	defaultCompactionThreshold = 0.8
	defaultSummaryMaxTokens    = 1024

	skillReplayTag = "<invoked_skills_replay>"
)

type CompactionStrategy interface {
	Name() string
	Compact(ctx context.Context, req CompactionRequest) (CompactionResult, error)
}

type CompactionRequest struct {
	Provider        core.Provider
	Model           core.ModelID
	System          []core.SystemBlock
	Tools           []core.ToolSchema
	Messages        []core.Message
	Trigger         string
	ContextWindow   int
	EstimatedTokens int
}

type CompactionResult struct {
	Messages []core.Message
	Changed  bool
}

type KeepLastTurnsCompactionStrategy struct {
	Turns                  int
	SummaryMaxOutputTokens int
}

func DefaultCompactionStrategy() CompactionStrategy {
	return KeepLastTurnsCompactionStrategy{}
}

func (s KeepLastTurnsCompactionStrategy) Name() string {
	turns := s.Turns
	if turns <= 0 {
		turns = defaultCompactionTurns
	}
	return fmt.Sprintf("keep-last-%d-turns", turns)
}

func (s KeepLastTurnsCompactionStrategy) Compact(ctx context.Context, req CompactionRequest) (CompactionResult, error) {
	turns := s.Turns
	if turns <= 0 {
		turns = defaultCompactionTurns
	}
	keepStart := keepLastTurnsStart(req.Messages, turns)
	if keepStart <= 0 {
		return CompactionResult{}, nil
	}
	if keepStart == 1 && isCompactionSummary(req.Messages[0]) {
		return CompactionResult{}, nil
	}
	summary, err := s.summarize(ctx, req, req.Messages[:keepStart])
	if err != nil {
		return CompactionResult{}, err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return CompactionResult{}, nil
	}
	next := make([]core.Message, 0, 1+len(req.Messages)-keepStart)
	next = append(next, compactionSummaryMessage(summary))
	next = append(next, cloneMessages(req.Messages[keepStart:])...)
	return CompactionResult{Messages: next, Changed: true}, nil
}

func (s KeepLastTurnsCompactionStrategy) summarize(ctx context.Context, req CompactionRequest, messages []core.Message) (string, error) {
	if req.Provider == nil {
		return "", core.NewConfigError("compaction requires a provider")
	}
	maxTokens := s.SummaryMaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultSummaryMaxTokens
	}
	prompt := "Summarize the earlier conversation for a coding agent that will continue the same session.\n\n" +
		"Preserve user goals, constraints, decisions, files read or changed, tool results, errors, and unresolved work. " +
		"Do not add new instructions or claims.\n\n" +
		"Earlier conversation:\n\n" + renderMessagesForSummary(messages)
	stream, err := core.StreamProvider(ctx, req.Provider, core.ProviderRequest{
		Model: req.Model,
		System: []core.SystemBlock{{
			Type: "text",
			Text: "You compact conversation history. Return only a concise continuation summary.",
		}},
		Messages:        []core.Message{{Role: "user", Content: []core.ContentBlock{core.Text(prompt)}}},
		MaxOutputTokens: &maxTokens,
		MaxRetries:      0,
	})
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for result := range stream {
		if result.Err != nil {
			return "", result.Err
		}
		if result.Event.Type == "text_delta" {
			text.WriteString(result.Event.Text)
		}
	}
	return text.String(), nil
}

func keepLastTurnsStart(messages []core.Message, turns int) int {
	if turns <= 0 {
		turns = defaultCompactionTurns
	}
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if !startsUserTurn(messages[i]) {
			continue
		}
		seen++
		if seen == turns {
			return i
		}
	}
	return 0
}

func startsUserTurn(msg core.Message) bool {
	if msg.Role != "user" {
		return false
	}
	if isCompactionSummary(msg) || isSkillReplayMessage(msg) {
		return false
	}
	hasContent := false
	for _, block := range msg.Content {
		if block.Type != core.BlockToolResult {
			hasContent = true
		}
	}
	return hasContent
}

func compactionSummaryMessage(summary string) core.Message {
	return core.Message{
		Role: "user",
		Content: []core.ContentBlock{core.Text(
			"<conversation_summary>\n" + strings.TrimSpace(summary) + "\n</conversation_summary>",
		)},
	}
}

func isCompactionSummary(msg core.Message) bool {
	return msg.Role == "user" &&
		len(msg.Content) == 1 &&
		msg.Content[0].Type == core.BlockText &&
		strings.HasPrefix(strings.TrimSpace(msg.Content[0].Text), "<conversation_summary>")
}

func injectSkillReplayMessages(messages []core.Message, skills []core.InvokedSkillRecord) []core.Message {
	msg, ok := skillReplayMessage(skills)
	if !ok {
		return messages
	}
	out := make([]core.Message, 0, len(messages)+1)
	out = append(out, messages...)
	insertAt := 0
	if len(out) > 0 && isCompactionSummary(out[0]) {
		insertAt = 1
	}
	out = append(out[:insertAt], append([]core.Message{msg}, out[insertAt:]...)...)
	return out
}

func stripProviderOnlyCompactionMessages(messages []core.Message) []core.Message {
	out := make([]core.Message, 0, len(messages))
	for _, msg := range messages {
		if isSkillReplayMessage(msg) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func skillReplayMessage(skills []core.InvokedSkillRecord) (core.Message, bool) {
	var filtered []core.InvokedSkillRecord
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" && strings.TrimSpace(skill.SubstitutedBody) == "" {
			continue
		}
		filtered = append(filtered, skill)
	}
	if len(filtered) == 0 {
		return core.Message{}, false
	}
	var b strings.Builder
	b.WriteString(skillReplayTag)
	b.WriteString("\n<skill_listing>\n")
	for _, skill := range filtered {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			name = "unnamed"
		}
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	b.WriteString("</skill_listing>\n")
	for _, skill := range filtered {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			name = "unnamed"
		}
		b.WriteString("<invoked_skill name=\"")
		b.WriteString(escapeSkillAttr(name))
		if skill.InvokedAt != 0 {
			b.WriteString("\" invoked_at=\"")
			b.WriteString(fmt.Sprint(skill.InvokedAt))
		}
		b.WriteString("\">\n")
		b.WriteString(strings.TrimSpace(skill.SubstitutedBody))
		b.WriteString("\n</invoked_skill>\n")
	}
	b.WriteString("</invoked_skills_replay>")
	return core.Message{Role: "user", Content: []core.ContentBlock{core.Text(b.String())}}, true
}

func isSkillReplayMessage(msg core.Message) bool {
	return msg.Role == "user" &&
		len(msg.Content) == 1 &&
		msg.Content[0].Type == core.BlockText &&
		strings.HasPrefix(strings.TrimSpace(msg.Content[0].Text), skillReplayTag)
}

func escapeSkillAttr(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

func renderMessagesForSummary(messages []core.Message) string {
	var b strings.Builder
	for i, msg := range messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.ToUpper(msg.Role))
		b.WriteString(":\n")
		for _, block := range msg.Content {
			b.WriteString(renderBlockForSummary(block))
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func renderBlockForSummary(block core.ContentBlock) string {
	switch block.Type {
	case core.BlockText:
		return block.Text
	case core.BlockThinking:
		return "[thinking] " + block.Thinking
	case core.BlockToolUse:
		raw, _ := json.Marshal(block.Input)
		return fmt.Sprintf("[tool_use id=%s name=%s input=%s]", block.ID, block.Name, raw)
	case core.BlockToolResult:
		return fmt.Sprintf("[tool_result id=%s error=%t content=%s]", block.ToolUseID, block.IsError, block.StringContent())
	case core.BlockImage:
		if block.Source == nil {
			return "[image]"
		}
		if block.Source.URL != "" {
			return "[image url=" + block.Source.URL + "]"
		}
		return "[image media_type=" + block.Source.MediaType + "]"
	default:
		raw, _ := json.Marshal(block)
		return string(raw)
	}
}

func cloneMessages(messages []core.Message) []core.Message {
	out := make([]core.Message, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].Content = append([]core.ContentBlock(nil), messages[i].Content...)
	}
	return out
}
