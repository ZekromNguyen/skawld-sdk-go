package skawld

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/skills"
)

func (s *Session) invokeSkill(ctx context.Context, inv core.SkillInvocation, emitter *eventEmitter) (core.ToolResult, error) {
	if s.agent.skills == nil {
		return core.ToolResult{Content: "No skills are loaded.", Summary: "Invoke skill " + inv.Name, IsError: true}, nil
	}
	def, ok := s.agent.skills.Get(inv.Name)
	if !ok {
		return core.ToolResult{Content: fmt.Sprintf("Skill %q is not loaded.", inv.Name), Summary: "Invoke skill " + inv.Name, IsError: true}, nil
	}
	body := skills.Substitute(def, inv.Arguments)
	now := time.Now().UTC().UnixMilli()
	record := core.InvokedSkillRecord{Name: def.Name, SubstitutedBody: body, InvokedAt: now}
	s.skillMu.Lock()
	s.invokedSkills = append(s.invokedSkills, record)
	s.pendingSkillOverlay = &skillOverlay{
		Name:         def.Name,
		Body:         body,
		Model:        def.Model,
		AllowedTools: append([]string(nil), def.AllowedTools...),
		Arguments:    inv.Arguments,
		InvokedAt:    now,
	}
	s.skillMu.Unlock()
	if err := s.store.SetInvokedSkills(ctx, s.ID, append([]core.InvokedSkillRecord(nil), s.invokedSkills...)); err != nil {
		return core.ToolResult{}, err
	}
	if !emitter.Emit(core.Event{Type: core.EventSkillInvoked, Subtype: def.Name, Delta: map[string]interface{}{"skill": def.Name, "arguments": inv.Arguments, "model": string(def.Model), "allowed_tools": def.AllowedTools}}) {
		return core.ToolResult{Content: "Skill invocation aborted.", Summary: "Skill " + def.Name + " invoked", IsError: true}, nil
	}
	if !emitter.Emit(core.Event{Type: core.EventSkillCompleted, Subtype: def.Name, Delta: map[string]interface{}{"skill": def.Name}}) {
		return core.ToolResult{Content: "Skill invocation aborted.", Summary: "Skill " + def.Name + " invoked", IsError: true}, nil
	}
	return core.ToolResult{Content: fmt.Sprintf("Skill %q loaded for the next assistant turn.", def.Name), Summary: "Skill " + def.Name + " invoked"}, nil
}

func (s *Session) consumePendingSkillOverlay() *skillOverlay {
	s.skillMu.Lock()
	defer s.skillMu.Unlock()
	overlay := s.pendingSkillOverlay
	s.pendingSkillOverlay = nil
	s.activeSkillOverlay = overlay
	return cloneSkillOverlay(overlay)
}

func (s *Session) clearActiveSkillOverlay() {
	s.skillMu.Lock()
	defer s.skillMu.Unlock()
	s.activeSkillOverlay = nil
}

func (s *Session) skillAllowsTool(name string) bool {
	s.skillMu.Lock()
	defer s.skillMu.Unlock()
	if s.activeSkillOverlay == nil {
		return false
	}
	for _, allowed := range s.activeSkillOverlay.AllowedTools {
		if allowed == "*" || allowed == name {
			return true
		}
	}
	return false
}

func cloneSkillOverlay(in *skillOverlay) *skillOverlay {
	if in == nil {
		return nil
	}
	out := *in
	out.AllowedTools = append([]string(nil), in.AllowedTools...)
	return &out
}

func skillOverlaySystemText(overlay *skillOverlay) string {
	var b strings.Builder
	b.WriteString("Active skill for this assistant turn: ")
	b.WriteString(overlay.Name)
	b.WriteString("\n\n")
	b.WriteString(overlay.Body)
	if len(overlay.AllowedTools) > 0 {
		b.WriteString("\n\nAllowed tools added for this turn: ")
		b.WriteString(strings.Join(overlay.AllowedTools, ", "))
	}
	return b.String()
}
