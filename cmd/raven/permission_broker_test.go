package main

import (
	"context"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/permissions"
)

func TestPermissionBrokerDeliversInteractiveDecision(t *testing.T) {
	broker := newPermissionBroker(true)
	response := make(chan permissions.CanUseToolResponse, 1)
	go func() {
		result, _ := broker.CanUseTool(context.Background(), permissions.CanUseToolRequest{ToolUseID: "call-1"})
		response <- result
	}()
	broker.Resolve("call-1", permissions.CanUseToolResponse{Behavior: "allow"})
	select {
	case result := <-response:
		if result.Behavior != "allow" {
			t.Fatalf("unexpected response: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("permission callback remained blocked")
	}
}

func TestPermissionBrokerAcceptsEarlyBatchDecision(t *testing.T) {
	broker := newPermissionBroker(true)
	broker.Resolve("call-2", permissions.CanUseToolResponse{Behavior: "deny", Message: "no"})
	result, err := broker.CanUseTool(context.Background(), permissions.CanUseToolRequest{ToolUseID: "call-2"})
	if err != nil || result.Behavior != "deny" {
		t.Fatalf("early decision was not delivered: result=%+v err=%v", result, err)
	}
}

func TestPermissionBrokerAllowEditsDoesNotAllowHighRiskExec(t *testing.T) {
	broker := newPermissionBroker(true)
	broker.EnableEdits()
	edit, err := broker.CanUseTool(context.Background(), permissions.CanUseToolRequest{
		ToolUseID: "edit", Descriptor: core.ToolDescriptor{Risk: core.RiskMedium, SideEffect: core.SideEffectIdempotent},
	})
	if err != nil || edit.Behavior != "allow" {
		t.Fatalf("expected edit to be allowed: %+v %v", edit, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := broker.CanUseTool(ctx, permissions.CanUseToolRequest{
		ToolUseID: "bash", Descriptor: core.ToolDescriptor{Risk: core.RiskHigh, SideEffect: core.SideEffectUnknown},
	}); err == nil {
		t.Fatal("allow edits incorrectly auto-approved high-risk execution")
	}
}

func TestPermissionBrokerFailsClosedOutsideInteractiveMode(t *testing.T) {
	broker := newPermissionBroker(false)
	result, err := broker.CanUseTool(context.Background(), permissions.CanUseToolRequest{ToolUseID: "call"})
	if err != nil || result.Behavior != "deny" {
		t.Fatalf("single-shot broker did not fail closed: result=%+v err=%v", result, err)
	}
}
