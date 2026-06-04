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

func (s *Session) runLoop(ctx context.Context, prompt string, opts RunOptions, out chan<- core.Event) {
	started := time.Now()
	runID := id.New()
	total := core.Usage{}
	agent := s.agent
	out <- core.Event{Type: core.EventSystem, Subtype: "init", SessionID: s.ID, RunID: runID, Model: agent.opts.Model, Tools: agent.opts.Tools.Names(), PermissionMode: agent.opts.Permissions.Mode, CWD: agent.opts.CWD}
	userMsg := buildUserMessage(prompt, opts.Images)
	if err := s.append([]core.Message{userMsg}); err != nil {
		emitRunError(out, err, total, started)
		return
	}
	out <- core.Event{Type: core.EventUser, Message: userMsg}

	for turn := 0; turn < agent.opts.MaxTurns; turn++ {
		if ctx.Err() != nil {
			out <- core.Event{Type: core.EventResult, Subtype: "aborted", StopReason: core.StopError, TotalUsage: total, DurationMS: time.Since(started).Milliseconds()}
			return
		}
		req := s.buildProviderRequest(ctx, opts)
		assistant, stop, usage, err := s.streamTurn(ctx, req, out)
		if err != nil {
			if isAbortError(ctx, err) {
				out <- core.Event{Type: core.EventResult, Subtype: "aborted", StopReason: core.StopError, TotalUsage: total, DurationMS: time.Since(started).Milliseconds()}
				return
			}
			emitRunError(out, err, total, started)
			return
		}
		if err := s.append([]core.Message{assistant}); err != nil {
			emitRunError(out, err, total, started)
			return
		}
		out <- core.Event{Type: core.EventAssistant, Message: assistant, StopReason: stop}
		total = core.AddUsage(total, usage)
		s.lastUsage = usage
		out <- core.Event{Type: core.EventUsage, Usage: usage, Cumulative: total}
		if stop != core.StopToolUse {
			out <- core.Event{Type: core.EventResult, Subtype: "success", StopReason: stop, TotalUsage: total, DurationMS: time.Since(started).Milliseconds(), FinalText: firstText(assistant)}
			return
		}
		results := s.executeToolCalls(ctx, runID, toolUseBlocks(assistant), out)
		resultMsg := core.Message{Role: "user", Content: results}
		if err := s.append([]core.Message{resultMsg}); err != nil {
			emitRunError(out, err, total, started)
			return
		}
		out <- core.Event{Type: core.EventUser, Message: resultMsg}
	}
	out <- core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: "TurnLimitError", Message: "max turns exceeded"}}
	out <- core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError, TotalUsage: total, DurationMS: time.Since(started).Milliseconds()}
}

func (s *Session) buildProviderRequest(ctx context.Context, opts RunOptions) core.ProviderRequest {
	s.providerMu.Lock()
	msgs := append([]core.Message(nil), s.providerView...)
	s.providerMu.Unlock()
	maxOut := s.agent.opts.MaxOutputTokens
	if opts.MaxOutputTokens != nil {
		maxOut = opts.MaxOutputTokens
	}
	return core.ProviderRequest{
		Model: s.agent.opts.Model, System: s.agent.system,
		Tools: s.agent.opts.Tools.Schemas(), Messages: msgs,
		MaxOutputTokens: maxOut, Temperature: opts.Temperature,
		CachePrompt: true, Thinking: opts.Thinking, Effort: opts.Effort,
		MaxRetries: s.agent.opts.MaxRetries,
	}
}

func (s *Session) streamTurn(ctx context.Context, req core.ProviderRequest, out chan<- core.Event) (core.Message, core.StopReason, core.Usage, error) {
	attempts := req.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		msg, stop, usage, committed, err := s.streamTurnAttempt(ctx, req, out)
		if err == nil {
			return msg, stop, usage, nil
		}
		lastErr = err
		if ctx.Err() != nil || committed || !isRetryable(err) || attempt == attempts-1 {
			break
		}
	}
	return core.Message{}, core.StopError, core.Usage{}, lastErr
}

func (s *Session) streamTurnAttempt(ctx context.Context, req core.ProviderRequest, out chan<- core.Event) (core.Message, core.StopReason, core.Usage, bool, error) {
	events, errs := s.agent.opts.Provider.Stream(ctx, req)
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
	for ev := range events {
		switch ev.Type {
		case "text_delta":
			committed = true
			flushThinking()
			textBuf += ev.Text
			if s.agent.opts.IncludePartialMessages {
				out <- core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "text", "text": ev.Text}}
			}
		case "thinking_delta":
			committed = true
			flushText()
			thinkingBuf += ev.Text
			if ev.Signature != "" {
				thinkingSig = ev.Signature
			}
			if s.agent.opts.IncludePartialMessages {
				out <- core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "thinking", "text": ev.Text}}
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
				out <- core.Event{Type: core.EventPartialAssistant, Delta: map[string]interface{}{"kind": "tool_use_input", "tool_use_id": ev.ID, "json_delta": ev.JSONDelta}}
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
	}
	if err := <-errs; err != nil {
		return core.Message{}, core.StopError, usage, committed, err
	}
	flushText()
	flushThinking()
	msg := core.Message{Role: "assistant", Content: content}
	if !providerMetadata.Empty() {
		msg.ProviderMetadata = providerMetadata
	}
	return msg, stop, usage, committed, nil
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

func (s *Session) executeToolCalls(ctx context.Context, runID string, blocks []core.ContentBlock, out chan<- core.Event) []core.ContentBlock {
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
		calls := s.resolveToolCallPermissions(ctx, batch.calls, results, out)
		if !batch.parallel {
			for _, call := range calls {
				if results[call.index].Type == core.BlockToolResult {
					continue
				}
				results[call.index] = s.executePreparedToolCall(ctx, runID, call, out)
			}
			continue
		}
		s.executeParallelBatch(ctx, runID, calls, results, out)
	}
	return results
}

func (s *Session) resolveToolCallPermissions(ctx context.Context, calls []scheduledToolCall, results []core.ContentBlock, out chan<- core.Event) []scheduledToolCall {
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
		decision := s.agent.perm.Evaluate(permissions.PendingCall{ToolUseID: call.block.ID, Tool: call.tool, Input: call.input, CWD: s.agent.opts.CWD})
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
	out <- core.Event{Type: core.EventPermissionRequest, Requests: requests}
	for _, call := range asks {
		decision := s.agent.perm.Resolve(ctx, permissions.PendingCall{ToolUseID: call.block.ID, Tool: call.tool, Input: call.input, CWD: s.agent.opts.CWD})
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

func (s *Session) executePreparedToolCall(ctx context.Context, runID string, call scheduledToolCall, out chan<- core.Event) core.ContentBlock {
	if call.tool == nil {
		return core.ToolResultBlock(call.block.ID, "Tool call could not be resolved", true)
	}
	out <- core.Event{Type: core.EventToolCallStart, ToolUseID: call.block.ID, ToolName: call.tool.Name(), Input: call.input}
	start := time.Now()
	res, err := call.tool.Execute(call.input, core.ToolContext{
		Context: ctx, CWD: s.agent.opts.CWD, FileReadTracker: s.readTracker,
		SessionID: s.ID, RunID: runID, SessionStore: s.store,
		Emit: func(ev core.Event) { out <- ev },
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
	out <- core.Event{Type: core.EventToolCallEnd, ToolUseID: call.block.ID, ToolName: call.tool.Name(), IsError: isErr, DurationMS: time.Since(start).Milliseconds()}
	return core.ToolResultBlock(call.block.ID, content, isErr)
}

func (s *Session) executeParallelBatch(ctx context.Context, runID string, calls []scheduledToolCall, results []core.ContentBlock, out chan<- core.Event) {
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
				resultCh <- pair{index: call.index, block: core.ToolResultBlock(call.block.ID, "Tool call aborted.", true)}
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
					out <- ev
				}
			}()
			block := s.executePreparedToolCall(ctx, runID, call, wrappedOut)
			close(wrappedOut)
			<-doneForward
			resultCh <- pair{index: call.index, block: block}
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
						out <- core.Event{Type: core.EventToolCallEnd, ToolUseID: id, ToolName: call.tool.Name(), IsError: true}
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

func emitRunError(out chan<- core.Event, err error, usage core.Usage, started time.Time) {
	name := "Error"
	retryable := false
	var skerr *core.SkawldError
	if errors.As(err, &skerr) {
		name = string(skerr.Kind)
		retryable = skerr.Retryable
	}
	out <- core.Event{Type: core.EventError, Error: &core.EventErrorPayload{Name: name, Message: err.Error(), Retryable: retryable}}
	out <- core.Event{Type: core.EventResult, Subtype: "error", StopReason: core.StopError, TotalUsage: usage, DurationMS: time.Since(started).Milliseconds()}
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
