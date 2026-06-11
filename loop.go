package skawld

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skawld/skawld-sdk-go/core"
	"github.com/skawld/skawld-sdk-go/internal/id"
	"github.com/skawld/skawld-sdk-go/permissions"
)

type eventEmitter struct {
	ctx context.Context
	out chan<- core.Event
}

func newEventEmitter(ctx context.Context, out chan<- core.Event) *eventEmitter {
	return &eventEmitter{ctx: ctx, out: out}
}

func (e *eventEmitter) Emit(ev core.Event) bool {
	if e == nil || e.out == nil {
		return false
	}
	select {
	case <-e.ctx.Done():
		return false
	default:
	}
	select {
	case e.out <- ev:
		return true
	default:
	}
	select {
	case e.out <- ev:
		return true
	case <-e.ctx.Done():
		return false
	}
}

func (s *Session) runLoop(ctx context.Context, prompt string, opts RunOptions, emitter *eventEmitter) {
	started := time.Now()
	runID := id.New()
	total := core.Usage{}
	agent := s.agent
	initialEvents, err := agent.loadRuntime(ctx)
	if err != nil {
		emitRunError(emitter, err, total, started)
		return
	}
	if !emitter.Emit(core.Event{Type: core.EventSystem, Subtype: "init", SessionID: s.ID, RunID: runID, Model: agent.opts.Model, Tools: agent.opts.Tools.Names(), PermissionMode: agent.opts.Permissions.Mode, CWD: agent.opts.CWD}) {
		return
	}
	for _, ev := range initialEvents {
		if !emitter.Emit(ev) {
			return
		}
	}
	for _, ev := range s.consumeInitialEvents() {
		if !emitter.Emit(ev) {
			return
		}
	}
	userMsg := buildUserMessage(prompt, opts.Images)
	if err := s.append(ctx, []core.Message{userMsg}); err != nil {
		if isAbortError(ctx, err) {
			_ = emitter.Emit(abortedResult(total, started))
			return
		}
		emitRunError(emitter, err, total, started)
		return
	}
	if !emitter.Emit(core.Event{Type: core.EventUser, Message: userMsg}) {
		return
	}

	for turn := 0; turn < agent.opts.MaxTurns; turn++ {
		if ctx.Err() != nil {
			_ = emitter.Emit(abortedResult(total, started))
			return
		}
		if _, err := s.compactProviderHistory(ctx, runID, compactionTriggerProactive, emitter); err != nil {
			emitRunError(emitter, err, total, started)
			return
		}
		overlay := s.consumePendingSkillOverlay()
		req := s.buildProviderRequest(ctx, opts, overlay)
		assistant, stop, usage, err := s.streamTurn(ctx, runID, req, emitter)
		if err != nil {
			if isAbortError(ctx, err) {
				_ = emitter.Emit(abortedResult(total, started))
				return
			}
			if isContextLengthError(err) {
				compacted, compactErr := s.compactProviderHistory(ctx, runID, compactionTriggerForced, emitter)
				if compactErr != nil {
					emitRunError(emitter, compactErr, total, started)
					return
				}
				if compacted {
					req = s.buildProviderRequest(ctx, opts, overlay)
					assistant, stop, usage, err = s.streamTurn(ctx, runID, req, emitter)
					if err == nil {
						goto turnSucceeded
					}
					if isAbortError(ctx, err) {
						_ = emitter.Emit(abortedResult(total, started))
						return
					}
				}
			}
			emitRunError(emitter, err, total, started)
			return
		}
	turnSucceeded:
		if err := s.append(ctx, []core.Message{assistant}); err != nil {
			if isAbortError(ctx, err) {
				_ = emitter.Emit(abortedResult(total, started))
				return
			}
			emitRunError(emitter, err, total, started)
			return
		}
		if !emitter.Emit(core.Event{Type: core.EventAssistant, Message: assistant, StopReason: stop}) {
			return
		}
		total = core.AddUsage(total, usage)
		s.lastUsage = usage
		if !emitter.Emit(core.Event{Type: core.EventUsage, Usage: usage, Cumulative: total}) {
			return
		}
		if stop != core.StopToolUse {
			s.clearActiveSkillOverlay()
			_ = emitter.Emit(core.Event{Type: core.EventResult, Subtype: "success", StopReason: stop, TotalUsage: total, DurationMS: time.Since(started).Milliseconds(), FinalText: firstText(assistant)})
			return
		}
		results := s.executeToolCalls(ctx, runID, toolUseBlocks(assistant), emitter)
		s.clearActiveSkillOverlay()
		resultMsg := core.Message{Role: "user", Content: results}
		if err := s.append(ctx, []core.Message{resultMsg}); err != nil {
			if isAbortError(ctx, err) {
				_ = emitter.Emit(abortedResult(total, started))
				return
			}
			emitRunError(emitter, err, total, started)
			return
		}
		if !emitter.Emit(core.Event{Type: core.EventUser, Message: resultMsg}) {
			return
		}
	}
	if !emitter.Emit(core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: "TurnLimitError", Message: "max turns exceeded"}}) {
		return
	}
	_ = emitter.Emit(core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError, TotalUsage: total, DurationMS: time.Since(started).Milliseconds()})
}

func (s *Session) buildProviderRequest(ctx context.Context, opts RunOptions, overlay *skillOverlay) core.ProviderRequest {
	s.providerMu.Lock()
	msgs := append([]core.Message(nil), s.providerHistory...)
	s.providerMu.Unlock()
	model := s.agent.opts.Model
	system := s.agent.systemBlocks()
	if overlay != nil {
		if overlay.Model != "" {
			model = overlay.Model
		}
		system = append(system, core.SystemBlock{Type: "text", Text: skillOverlaySystemText(overlay)})
	}
	maxOut := s.agent.opts.MaxOutputTokens
	if opts.MaxOutputTokens != nil {
		maxOut = opts.MaxOutputTokens
	}
	return core.ProviderRequest{
		Model: model, System: system,
		Tools: s.agent.toolSchemas(), Messages: msgs,
		MaxOutputTokens: maxOut, Temperature: opts.Temperature,
		CachePrompt: true, Thinking: opts.Thinking, Effort: opts.Effort,
		MaxRetries: s.agent.opts.MaxRetries,
	}
}

func (s *Session) streamTurn(ctx context.Context, runID string, req core.ProviderRequest, emitter *eventEmitter) (core.Message, core.StopReason, core.Usage, error) {
	attempts := req.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		start := time.Now()
		msg, stop, usage, committed, err := s.streamTurnAttempt(ctx, req, emitter)
		s.agent.observe(ctx, core.Observation{
			Type:       core.ObservationProviderAttempt,
			Operation:  "stream",
			SessionID:  s.ID,
			RunID:      runID,
			ProviderID: s.agent.opts.Provider.ID(),
			Attempt:    attempt + 1,
			DurationMS: time.Since(start).Milliseconds(),
			Error:      err,
		})
		if err == nil {
			return msg, stop, usage, nil
		}
		lastErr = err
		if ctx.Err() != nil || committed || !isRetryable(err) || attempt == attempts-1 {
			break
		}
	}
	if lastErr != nil {
		return core.Message{}, core.StopError, core.Usage{}, fmt.Errorf("provider stream %s attempt failed: %w", s.agent.opts.Provider.ID(), lastErr)
	}
	return core.Message{}, core.StopError, core.Usage{}, lastErr
}

func (s *Session) streamTurnAttempt(ctx context.Context, req core.ProviderRequest, emitter *eventEmitter) (core.Message, core.StopReason, core.Usage, bool, error) {
	stream, err := core.StreamProvider(ctx, s.agent.opts.Provider, req)
	if err != nil {
		return core.Message{}, core.StopError, core.Usage{}, false, err
	}
	var content []core.ContentBlock
	textBuf := ""
	thinkingBuf := ""
	thinkingSig := ""
	toolMeta := map[string]string{}
	toolInput := map[string]string{}
	stop := core.StopEndTurn
	usage := core.Usage{}
	providerMetadata := core.MessageProviderMetadata{}
	committed := false
	flushText := func() {
		if textBuf != "" {
			content = append(content, core.Text(textBuf))
			textBuf = ""
		}
	}
	flushThinking := func() {
		if thinkingBuf != "" {
			content = append(content, core.ContentBlock{Type: core.BlockThinking, Thinking: thinkingBuf, Signature: thinkingSig})
			thinkingBuf = ""
			thinkingSig = ""
		}
	}
	for {
		select {
		case result, ok := <-stream:
			if !ok {
				if ctx.Err() != nil {
					return core.Message{}, core.StopError, usage, committed, core.NewAbortError("provider stream canceled", ctx.Err())
				}
				flushText()
				flushThinking()
				msg := core.Message{Role: "assistant", Content: content}
				if !providerMetadata.Empty() {
					msg.ProviderMetadata = providerMetadata
				}
				return msg, stop, usage, committed, nil
			}
			if result.Err != nil {
				return core.Message{}, core.StopError, usage, committed, result.Err
			}
			ev := result.Event
			switch ev.Type {
			case "text_delta":
				committed = true
				flushThinking()
				textBuf += ev.Text
				if s.agent.opts.IncludePartialMessages {
					if !emitter.Emit(core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "text", "text": ev.Text}}) {
						return core.Message{}, core.StopError, usage, committed, core.NewAbortError("run event stream closed", nil)
					}
				}
			case "thinking_delta":
				committed = true
				flushText()
				thinkingBuf += ev.Text
				if ev.Signature != "" {
					thinkingSig = ev.Signature
				}
				if s.agent.opts.IncludePartialMessages {
					if !emitter.Emit(core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "thinking", "text": ev.Text}}) {
						return core.Message{}, core.StopError, usage, committed, core.NewAbortError("run event stream closed", nil)
					}
				}
			case "tool_use_start":
				committed = true
				flushText()
				flushThinking()
				toolMeta[ev.ID] = ev.Name
				toolInput[ev.ID] = ""
			case "tool_use_input_delta":
				committed = true
				toolInput[ev.ID] += ev.JSONDelta
				if s.agent.opts.IncludePartialMessages {
					if !emitter.Emit(core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "tool_use_input", "tool_use_id": ev.ID, "json_delta": ev.JSONDelta}}) {
						return core.Message{}, core.StopError, usage, committed, core.NewAbortError("run event stream closed", nil)
					}
				}
			case "tool_use_end":
				input := map[string]interface{}{}
				if raw := toolInput[ev.ID]; raw != "" {
					if err := json.Unmarshal([]byte(raw), &input); err != nil {
						input = map[string]interface{}{"__invalidJson": true, "raw": raw}
					}
				}
				content = append(content, core.ToolUse(ev.ID, toolMeta[ev.ID], input))
			case "message_end":
				flushText()
				flushThinking()
				stop = ev.StopReason
				usage = ev.Usage
				providerMetadata = ev.ProviderMetadata
			}
		case <-ctx.Done():
			return core.Message{}, core.StopError, usage, committed, core.NewAbortError("provider stream canceled", ctx.Err())
		}
	}
}

type scheduledToolCall struct {
	index int
	block core.ContentBlock
	tool  core.Tool
	input map[string]interface{}
}

type toolBatch struct {
	parallel bool
	calls    []scheduledToolCall
}

func (s *Session) executeToolCalls(ctx context.Context, runID string, blocks []core.ContentBlock, emitter *eventEmitter) []core.ContentBlock {
	results := make([]core.ContentBlock, len(blocks))
	var batches []toolBatch
	for i, b := range blocks {
		call := scheduledToolCall{index: i, block: b}
		tool, ok := s.agent.opts.Tools.Get(b.Name)
		if !ok {
			results[i] = core.ToolResultBlock(b.ID, fmt.Sprintf("Tool %q is not registered", b.Name), true)
			batches = append(batches, toolBatch{calls: []scheduledToolCall{call}})
			continue
		}
		input, err := tool.Validate(b.Input)
		if err != nil {
			results[i] = core.ToolResultBlock(b.ID, err.Error(), true)
			batches = append(batches, toolBatch{calls: []scheduledToolCall{call}})
			continue
		}
		call.tool = tool
		call.input = input
		parallel := tool.ParallelSafe()
		if parallel && len(batches) > 0 && batches[len(batches)-1].parallel {
			batches[len(batches)-1].calls = append(batches[len(batches)-1].calls, call)
		} else {
			batches = append(batches, toolBatch{parallel: parallel, calls: []scheduledToolCall{call}})
		}
	}
	for _, batch := range batches {
		calls := s.resolveToolCallPermissions(ctx, runID, batch.calls, results, emitter)
		if !batch.parallel {
			for _, call := range calls {
				if results[call.index].Type == core.BlockToolResult {
					continue
				}
				results[call.index] = s.executePreparedToolCall(ctx, runID, call, emitter)
			}
			continue
		}
		s.executeParallelBatch(ctx, runID, calls, results, emitter)
	}
	return results
}

func (s *Session) resolveToolCallPermissions(ctx context.Context, runID string, calls []scheduledToolCall, results []core.ContentBlock, emitter *eventEmitter) []scheduledToolCall {
	ready := make([]scheduledToolCall, 0, len(calls))
	asks := make([]scheduledToolCall, 0)
	requests := make([]core.PermissionRequest, 0)
	for _, call := range calls {
		if results[call.index].Type == core.BlockToolResult {
			continue
		}
		if call.tool == nil {
			results[call.index] = core.ToolResultBlock(call.block.ID, "Tool call could not be resolved", true)
			continue
		}
		if s.skillAllowsTool(call.tool.Name()) {
			ready = append(ready, call)
			continue
		}
		decision := s.agent.perm.Evaluate(permissions.PendingCall{ToolUseID: call.block.ID, Tool: call.tool, Input: call.input, CWD: s.agent.opts.CWD, SessionID: s.ID, RunID: runID})
		switch decision.Decision {
		case permissions.DecisionAllow:
			if decision.UpdatedInput != nil {
				call.input = decision.UpdatedInput
			}
			ready = append(ready, call)
		case permissions.DecisionAsk:
			asks = append(asks, call)
			requests = append(requests, core.PermissionRequest{ToolUseID: call.block.ID, ToolName: call.tool.Name(), Input: call.input, Summary: call.tool.Summarize(call.input)})
		case permissions.DecisionDeny:
			results[call.index] = core.ToolResultBlock(call.block.ID, "Tool call denied: "+decision.Reason, true)
		}
	}
	if len(requests) == 0 {
		return ready
	}
	if !emitter.Emit(core.Event{Type: core.EventPermissionRequest, Requests: requests}) {
		return ready
	}
	for _, call := range asks {
		decision := s.agent.perm.Resolve(ctx, permissions.PendingCall{ToolUseID: call.block.ID, Tool: call.tool, Input: call.input, CWD: s.agent.opts.CWD, SessionID: s.ID, RunID: runID})
		if decision.Decision == permissions.DecisionDeny {
			results[call.index] = core.ToolResultBlock(call.block.ID, "Tool call denied: "+decision.Reason, true)
			continue
		}
		if decision.UpdatedInput != nil {
			call.input = decision.UpdatedInput
		}
		ready = append(ready, call)
	}
	return ready
}

func (s *Session) executePreparedToolCall(ctx context.Context, runID string, call scheduledToolCall, emitter *eventEmitter) core.ContentBlock {
	if call.tool == nil {
		return core.ToolResultBlock(call.block.ID, "Tool call could not be resolved", true)
	}
	if !emitter.Emit(core.Event{Type: core.EventToolCallStart, ToolUseID: call.block.ID, ToolName: call.tool.Name(), Input: call.input}) {
		return core.ToolResultBlock(call.block.ID, "Tool call aborted.", true)
	}
	start := time.Now()
	res, err := call.tool.Execute(call.input, core.ToolContext{
		Context: ctx, CWD: s.agent.opts.CWD, Filesystem: s.agent.opts.FilesystemPolicy, FileReadTracker: s.readTracker,
		Observer: s.agent, SessionID: s.ID, RunID: runID, SessionStore: s.store,
		Emit: func(ev core.Event) { _ = emitter.Emit(ev) },
		InvokeSkill: func(skillCtx context.Context, inv core.SkillInvocation) (core.ToolResult, error) {
			return s.invokeSkill(skillCtx, inv, emitter)
		},
		RunSubagent: func(subCtx context.Context, inv core.SubagentInvocation) (core.ToolResult, error) {
			return s.runSubagent(subCtx, inv, emitter)
		},
	})
	isErr := false
	content := interface{}("")
	if err != nil {
		isErr = true
		content = "Tool failed: " + err.Error()
	} else {
		isErr = res.IsError
		content = res.Content
	}
	duration := time.Since(start).Milliseconds()
	if !emitter.Emit(core.Event{Type: core.EventToolCallEnd, ToolUseID: call.block.ID, ToolName: call.tool.Name(), IsError: isErr, DurationMS: duration}) {
		isErr = true
		content = "Tool call aborted."
	}
	var observedErr error
	if err != nil {
		observedErr = err
	}
	s.agent.observe(ctx, core.Observation{
		Type:       core.ObservationToolExecution,
		Operation:  "execute",
		SessionID:  s.ID,
		RunID:      runID,
		ToolName:   call.tool.Name(),
		DurationMS: duration,
		Error:      observedErr,
	})
	return core.ToolResultBlock(call.block.ID, content, isErr)
}

func (s *Session) executeParallelBatch(ctx context.Context, runID string, calls []scheduledToolCall, results []core.ContentBlock, emitter *eventEmitter) {
	concurrency := s.agent.opts.ToolConcurrency
	if concurrency <= 0 {
		concurrency = 8
	}
	sem := make(chan struct{}, concurrency)
	type pair struct {
		index int
		block core.ContentBlock
	}
	resultCh := make(chan pair, len(calls))
	started := map[string]scheduledToolCall{}
	finished := map[string]bool{}
	var stateMu sync.Mutex
	var wg sync.WaitGroup
	for _, call := range calls {
		if results[call.index].Type == core.BlockToolResult {
			continue
		}
		call := call
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				select {
				case resultCh <- pair{index: call.index, block: core.ToolResultBlock(call.block.ID, "Tool call aborted.", true)}:
				case <-emitter.ctx.Done():
				}
				return
			}
			wrappedOut := make(chan core.Event, 16)
			doneForward := make(chan struct{})
			go func() {
				defer close(doneForward)
				for ev := range wrappedOut {
					if ev.Type == core.EventToolCallStart {
						stateMu.Lock()
						started[ev.ToolUseID] = call
						stateMu.Unlock()
					}
					if ev.Type == core.EventToolCallEnd {
						stateMu.Lock()
						finished[ev.ToolUseID] = true
						stateMu.Unlock()
					}
					if !emitter.Emit(ev) {
						return
					}
				}
			}()
			block := s.executePreparedToolCall(ctx, runID, call, newEventEmitter(emitter.ctx, wrappedOut))
			close(wrappedOut)
			<-doneForward
			select {
			case resultCh <- pair{index: call.index, block: block}:
			case <-emitter.ctx.Done():
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultCh)
		close(done)
	}()
	aborted := false
	for {
		select {
		case p, ok := <-resultCh:
			if !ok {
				return
			}
			results[p.index] = p.block
		case <-ctx.Done():
			if !aborted {
				aborted = true
				stateMu.Lock()
				for id, call := range started {
					if !finished[id] {
						_ = emitter.Emit(core.Event{Type: core.EventToolCallEnd, ToolUseID: id, ToolName: call.tool.Name(), IsError: true})
						results[call.index] = core.ToolResultBlock(id, "Tool call aborted.", true)
					}
				}
				stateMu.Unlock()
			}
		case <-done:
			for p := range resultCh {
				results[p.index] = p.block
			}
			return
		}
	}
}

func toolUseBlocks(msg core.Message) []core.ContentBlock {
	var out []core.ContentBlock
	for _, b := range msg.Content {
		if b.Type == core.BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

func firstText(msg core.Message) string {
	for _, b := range msg.Content {
		if b.Type == core.BlockText {
			return b.Text
		}
	}
	return ""
}

func emitRunError(emitter *eventEmitter, err error, usage core.Usage, started time.Time) {
	name := "Error"
	retryable := false
	var skerr *core.SkawldError
	if errors.As(err, &skerr) {
		name = string(skerr.Kind)
		retryable = skerr.Retryable
	}
	if !emitter.Emit(core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: name, Message: err.Error(), Retryable: retryable}}) {
		return
	}
	_ = emitter.Emit(core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError, TotalUsage: usage, DurationMS: time.Since(started).Milliseconds()})
}

func abortedResult(usage core.Usage, started time.Time) core.Event {
	return core.Event{Type: core.EventResult, Subtype: "aborted", StopReason: core.StopError, TotalUsage: usage, DurationMS: time.Since(started).Milliseconds()}
}

func isAbortError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var skerr *core.SkawldError
	return errors.As(err, &skerr) && skerr.Kind == core.ErrorAbort
}

func isRetryable(err error) bool {
	var skerr *core.SkawldError
	return errors.As(err, &skerr) && skerr.Retryable
}

func isContextLengthError(err error) bool {
	var skerr *core.SkawldError
	return errors.As(err, &skerr) && skerr.Kind == core.ErrorContextLength
}
