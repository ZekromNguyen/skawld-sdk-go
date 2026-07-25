package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/audit"
	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type accountingTools struct {
	purchaseOrders map[string]map[string]interface{}
	reviewed       map[string]bool
}

func (t *accountingTools) Describe(ctx context.Context, name string) (core.ToolDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolDescriptor{}, false, err
	}
	switch name {
	case "accounting.lookup_purchase_order":
		return core.ToolDescriptor{
			Risk: core.RiskLow, SideEffect: core.SideEffectNone,
			Idempotency: core.IdempotencyNotApplicable, Timeout: time.Second,
			OutputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id":    map[string]interface{}{"type": "string"},
					"total": map[string]interface{}{"type": "number"},
				},
				"required": []interface{}{"id", "total"},
			},
		}, true, nil
	case "accounting.mark_invoice_reviewed":
		return core.ToolDescriptor{
			Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
			Idempotency: core.IdempotencyRequired, Timeout: time.Second,
			Permissions: []string{"invoice.review"},
			OutputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"invoice_id": map[string]interface{}{"type": "string"},
					"status":     map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"invoice_id", "status"},
			},
		}, true, nil
	default:
		return core.ToolDescriptor{}, false, nil
	}
}

func (t *accountingTools) Execute(ctx context.Context, name string, input map[string]interface{}, idempotencyKey string) (workflow.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.ToolResult{}, err
	}
	switch name {
	case "accounting.lookup_purchase_order":
		poID, _ := input["po_id"].(string)
		order, ok := t.purchaseOrders[poID]
		if !ok {
			return workflow.ToolResult{}, fmt.Errorf("purchase order %q not found", poID)
		}
		return workflow.ToolResult{Output: order}, nil
	case "accounting.mark_invoice_reviewed":
		invoiceID, _ := input["invoice_id"].(string)
		if idempotencyKey == "" {
			return workflow.ToolResult{}, fmt.Errorf("idempotency key is required")
		}
		t.reviewed[invoiceID] = true
		return workflow.ToolResult{Output: map[string]interface{}{"invoice_id": invoiceID, "status": "reviewed"}}, nil
	default:
		return workflow.ToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
}

func main() {
	ctx := context.Background()
	principal := core.Principal{
		TenantID: "demo-company", ActorID: "accountant@example.com",
		Roles: []string{"accountant"},
	}
	ctx = core.WithPrincipal(ctx, principal)

	traceStore := observation.NewMemoryStore()
	recorder, err := observation.NewRecorder(traceStore)
	if err != nil {
		log.Fatal(err)
	}
	demo, err := recorder.Start(ctx, "invoice-reconciliation", principal, map[string]interface{}{"environment": "fixture"})
	if err != nil {
		log.Fatal(err)
	}
	_, _ = recorder.Capture(ctx, demo.ID, observation.Event{
		Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
		Application: "invoice_demo", Action: "open_invoice", Intent: "inspect invoice",
		Entity: &observation.Entity{Type: "invoice", ID: "INV-1001"},
		Input:  map[string]interface{}{"invoice_id": "INV-1001"},
	})
	_, _ = recorder.Capture(ctx, demo.ID, observation.Event{
		Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
		Application: "purchase_order_demo", Action: "lookup_purchase_order", Intent: "compare invoice to purchase order",
		Entity: &observation.Entity{Type: "purchase_order", ID: "PO-42"},
		Input:  map[string]interface{}{"po_id": "PO-42"},
		Output: map[string]interface{}{"total": 500.0},
	})
	if _, err := recorder.Complete(ctx, demo.ID, map[string]interface{}{"totals_match": true}); err != nil {
		log.Fatal(err)
	}

	version := invoiceWorkflow(demo.ID)
	versionStore := workflow.NewMemoryStore()
	if _, err := versionStore.SaveCandidate(ctx, version); err != nil {
		log.Fatal(err)
	}
	version, err = versionStore.Publish(ctx, version.Workflow.ID, version.Version, principal)
	if err != nil {
		log.Fatal(err)
	}

	toolRunner := &accountingTools{
		purchaseOrders: map[string]map[string]interface{}{"PO-42": {"id": "PO-42", "total": 500.0}},
		reviewed:       make(map[string]bool),
	}
	approvalAuthorization, err := policy.NewApprovalRolePolicy(
		policy.ApprovalRolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"finance-approver": {"approval.grant"},
			},
			RequireDistinctApprover: true,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	approvals, err := policy.NewAuthorizedApprovalStore(
		policy.NewMemoryApprovalStore(), approvalAuthorization,
	)
	if err != nil {
		log.Fatal(err)
	}
	auditStore := &audit.MemoryStore{}
	executionStore := workflow.NewMemoryExecutionStore()
	authorization, err := policy.NewRolePolicy(policy.RolePolicyOptions{
		RoleCapabilities: map[string][]string{
			"accountant": {"invoice.review"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	executor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: toolRunner, Policy: authorization,
		Approvals: approvals, Audit: auditStore,
		Executions: executionStore,
	})
	if err != nil {
		log.Fatal(err)
	}
	execution, err := executor.Execute(ctx, version, map[string]interface{}{
		"invoice": map[string]interface{}{"id": "INV-1001", "po_id": "PO-42", "total": 500.0},
	}, nil, principal)
	if err != nil {
		log.Fatal(err)
	}
	if execution.Status == workflow.ExecutionAwaitingApproval {
		approver := core.Principal{
			TenantID: principal.TenantID,
			ActorID:  "controller@example.com",
			Roles:    []string{"finance-approver"},
		}
		if _, err := approvals.Decide(
			core.WithPrincipal(context.Background(), approver),
			execution.PendingApprovalID, policy.ApprovalGranted,
			approver, "fixture totals match",
		); err != nil {
			log.Fatal(err)
		}
		execution, err = executor.Resume(ctx, version, execution)
		if err != nil {
			log.Fatal(err)
		}
	}
	events, err := auditStore.List(ctx, execution.ID)
	if err != nil {
		log.Fatal(err)
	}
	output, _ := json.MarshalIndent(map[string]interface{}{
		"demonstration_id": demo.ID,
		"execution_id":     execution.ID,
		"revision":         execution.Revision,
		"status":           execution.Status,
		"invoice_reviewed": toolRunner.reviewed["INV-1001"],
		"audit_events":     len(events),
	}, "", "  ")
	fmt.Println(string(output))
}

func invoiceWorkflow(demonstrationID string) workflow.Version {
	totalsMatch := workflow.Condition{
		Left: workflow.Value{Ref: "input.invoice.total"}, Operator: workflow.OpEqual,
		Right: workflow.Value{Ref: "steps.lookup_po.output.total"},
	}
	idempotencyKey := workflow.Value{Ref: "input.invoice.id"}
	return workflow.Version{
		SchemaVersion: workflow.SchemaVersion,
		Workflow: workflow.Workflow{
			ID: "invoice-reconciliation", TenantID: "demo-company", Name: "Invoice reconciliation",
			Description: "Compare an invoice with its purchase order and mark a matching invoice reviewed after approval.",
		},
		Version: 1, Status: workflow.VersionCandidate, CreatedAt: time.Now().UTC(),
		CreatedBy:              principalName(),
		SourceDemonstrationIDs: []string{demonstrationID},
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"invoice": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":    map[string]interface{}{"type": "string"},
						"po_id": map[string]interface{}{"type": "string"},
						"total": map[string]interface{}{"type": "number"},
					},
					"required": []interface{}{"id", "po_id", "total"},
				},
			},
			"required": []interface{}{"invoice"},
		},
		Steps: []workflow.Step{
			{
				ID: "lookup_po", Kind: workflow.StepTool,
				Tool: &workflow.ToolCall{
					Name:      "accounting.lookup_purchase_order",
					Arguments: map[string]workflow.Value{"po_id": {Ref: "input.invoice.po_id"}},
					Reason:    "retrieve the purchase order referenced by the invoice",
				},
			},
			{
				ID: "validate_total", Kind: workflow.StepValidation, DependsOn: []string{"lookup_po"},
				Validation: &workflow.Validation{Condition: totalsMatch, Message: "invoice total does not match purchase order total"},
			},
			{
				ID: "mark_reviewed", Kind: workflow.StepTool, DependsOn: []string{"validate_total"},
				Tool: &workflow.ToolCall{
					Name:           "accounting.mark_invoice_reviewed",
					Arguments:      map[string]workflow.Value{"invoice_id": {Ref: "input.invoice.id"}},
					IdempotencyKey: &idempotencyKey,
					Reason:         "validated invoice and purchase-order totals match",
				},
			},
		},
	}
}

func principalName() string { return "human-reviewed-demo" }
