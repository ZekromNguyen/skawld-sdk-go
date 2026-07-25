package skawld

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/sessions"
	"github.com/ZekromNguyen/skawld-sdk-go/subagents"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

func subagentListingPrompt(defs []subagents.Definition) string {
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available subagents:\n\n")
	for _, def := range defs {
		b.WriteString("- ")
		b.WriteString(def.Name)
		if def.Description != "" {
			b.WriteString(": ")
			b.WriteString(def.Description)
		}
		if len(def.Tools) > 0 {
			b.WriteString("\n  Tools: ")
			b.WriteString(strings.Join(def.Tools, ", "))
		}
		if def.Model != "" {
			b.WriteString("\n  Model: ")
			b.WriteString(string(def.Model))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nUse the Subagent tool for isolated investigation, parallelizable research, or delegated implementation steps.")
	return b.String()
}

func (s *Session) runSubagent(ctx context.Context, inv core.SubagentInvocation, emitter *eventEmitter) (core.ToolResult, error) {
	if s.agent.subagents == nil {
		return core.ToolResult{Content: "No subagents are configured.", Summary: "Run subagent " + inv.Name, IsError: true}, nil
	}
	def, ok := s.agent.subagents.Get(inv.Name)
	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Subagent %q is not loaded.", inv.Name), Summary: "Run subagent " + inv.Name, IsError: true}, nil
	}
	reg, err := filteredToolRegistry(s.agent.opts.Tools, def.Tools)
	if err != nil {
		return core.ToolResult{}, err
	}
	model := s.agent.opts.Model
	if def.Model != "" {
		model = def.Model
	}
	started := time.Now()
	child, err := NewAgent(AgentOptions{
		Provider:               s.agent.providerForSubagent(),
		ProviderFactory:        s.agent.opts.ProviderFactory,
		Model:                  model,
		Tools:                  reg,
		Permissions:            s.agent.opts.Permissions,
		SessionStore:           sessions.NewInMemoryStore(),
		CWD:                    s.agent.opts.CWD,
		FilesystemPolicy:       s.agent.opts.FilesystemPolicy,
		SystemPrompt:           def.SystemPrompt,
		MaxRetries:             s.agent.opts.MaxRetries,
		MaxOutputTokens:        s.agent.opts.MaxOutputTokens,
		IncludePartialMessages: s.agent.opts.IncludePartialMessages,
		MaxTurns:               s.agent.opts.MaxTurns,
		ToolConcurrency:        s.agent.opts.ToolConcurrency,
		DisableCompaction:      true,
		SkillsDir:              s.agent.opts.SkillsDir,
		SubagentsDir:           s.agent.opts.SubagentsDir,
		DisableSkills:          !allowsTool(def.Tools, "Skill"),
		DisableSubagents:       !allowsTool(def.Tools, "Subagent"),
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	defer child.Close()
	childSession, err := child.Session(ctx, SessionOptions{Meta: map[string]interface{}{"parent_session_id": s.ID, "subagent": def.Name}})
	if err != nil {
		return core.ToolResult{}, err
	}
	var final string
	var stop core.StopReason
	var total core.Usage
	var isErr bool
	handle := childSession.StartRun(ctx, inv.Task, RunOptions{})
	defer handle.Close()
	for ev := range handle.Events() {
		if !emitter.Emit(wrapSubagentEvent(def.Name, ev)) {
			handle.Close()
			return core.ToolResult{Content: "Subagent run aborted.", Summary: "Run subagent " + inv.Name, IsError: true}, nil
		}
		if ev.Type == core.EventResult {
			final = ev.FinalText
			stop = ev.StopReason
			total = ev.TotalUsage
			isErr = ev.Subtype == "error" || ev.Subtype == "aborted"
		}
		if ev.Type == core.EventError && ev.Error != nil {
			isErr = true
		}
	}
	if final == "" && isErr {
		final = "Subagent did not produce final text."
	}
	summary := fmt.Sprintf("Subagent %s completed in %dms", def.Name, time.Since(started).Milliseconds())
	if stop != "" {
		summary += " with stop reason " + string(stop)
	}
	_ = total
	return core.ToolResult{Content: final, Summary: summary, IsError: isErr}, nil
}

func wrapSubagentEvent(name string, ev core.Event) core.Event {
	return core.Event{Type: core.EventSubagent, Subtype: name, Delta: map[string]interface{}{"agent": name, "event": ev}}
}

func filteredToolRegistry(source *tools.Registry, allowed []string) (*tools.Registry, error) {
	reg := tools.NewRegistry()
	allowAll := len(allowed) == 0
	allowedSet := map[string]struct{}{}
	for _, name := range allowed {
		if name == "*" {
			allowAll = true
			continue
		}
		allowedSet[name] = struct{}{}
	}
	for _, tool := range source.List() {
		if !allowAll {
			if _, ok := allowedSet[tool.Name()]; !ok {
				continue
			}
		}
		if err := reg.Register(tool); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func allowsTool(allowed []string, name string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedName := range allowed {
		if allowedName == "*" || allowedName == name {
			return true
		}
	}
	return false
}
