// Command learned_invoice demonstrates the complete local lifecycle:
// semantic demonstrations -> structured extraction -> compilation -> evaluation
// -> human review -> publication -> deterministic resolution -> approval ->
// execution. The model provider and business systems are fixtures so the
// example is deterministic and requires no credentials.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/evaluation"
	"github.com/ZekromNguyen/skawld-sdk-go/learning"
	"github.com/ZekromNguyen/skawld-sdk-go/learning/structured"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/policy"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

const (
	lookupToolName = "accounting.lookup_purchase_order"
	markToolName   = "accounting.mark_invoice_reviewed"
)

type fixtureProvider struct {
	document string
}

func (fixtureProvider) ID() string                     { return "fixture-structured-provider" }
func (fixtureProvider) ContextWindow(core.ModelID) int { return 128000 }
func (p fixtureProvider) Stream(ctx context.Context, _ core.ProviderRequest) core.ProviderStream {
	output := make(chan core.ProviderStreamResult, 5)
	go func() {
		defer close(output)
		events := []core.ProviderStreamEvent{
			{Type: "message_start", Model: "fixture-model"},
			{Type: "tool_use_start", ID: "candidate-1", Name: "submit_workflow_candidate"},
			{Type: "tool_use_input_delta", ID: "candidate-1", JSONDelta: p.document},
			{Type: "tool_use_end", ID: "candidate-1"},
			{
				Type: "message_end", StopReason: core.StopToolUse,
				Usage: core.Usage{InputTokens: 100, OutputTokens: 40},
			},
		}
		for _, event := range events {
			select {
			case output <- core.ProviderStreamResult{Event: event}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return output
}

type lookupPurchaseOrderTool struct {
	orders map[string]map[string]interface{}
}

func (lookupPurchaseOrderTool) Name() string          { return lookupToolName }
func (lookupPurchaseOrderTool) Description() string   { return "Look up a purchase order by ID." }
func (lookupPurchaseOrderTool) Scope() core.ToolScope { return core.ToolScopeRead }
func (lookupPurchaseOrderTool) ParallelSafe() bool    { return true }
func (lookupPurchaseOrderTool) InputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"po_id": map[string]interface{}{"type": "string"},
	}, "po_id")
}
func (lookupPurchaseOrderTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskLow, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
		OutputSchema: objectSchema(map[string]interface{}{
			"id":    map[string]interface{}{"type": "string"},
			"total": map[string]interface{}{"type": "number"},
		}, "id", "total"),
	}
}
func (lookupPurchaseOrderTool) Validate(input map[string]interface{}) (map[string]interface{}, error) {
	if value, ok := input["po_id"].(string); !ok || value == "" {
		return nil, fmt.Errorf("po_id is required")
	}
	return input, nil
}
func (t lookupPurchaseOrderTool) Execute(
	input map[string]interface{},
	ctx core.ToolContext,
) (core.ToolResult, error) {
	if err := ctx.Context.Err(); err != nil {
		return core.ToolResult{}, err
	}
	order, exists := t.orders[input["po_id"].(string)]
	if !exists {
		return core.ToolResult{}, fmt.Errorf("purchase order not found")
	}
	return core.ToolResult{Content: order, Summary: "purchase order found"}, nil
}
func (lookupPurchaseOrderTool) Summarize(map[string]interface{}) string {
	return "look up purchase order"
}

type reviewLedger struct {
	mu       sync.Mutex
	reviewed map[string]bool
}

type markInvoiceReviewedTool struct {
	ledger *reviewLedger
}

func (markInvoiceReviewedTool) Name() string          { return markToolName }
func (markInvoiceReviewedTool) Description() string   { return "Mark a validated invoice as reviewed." }
func (markInvoiceReviewedTool) Scope() core.ToolScope { return core.ToolScopeWrite }
func (markInvoiceReviewedTool) ParallelSafe() bool    { return true }
func (markInvoiceReviewedTool) InputSchema() map[string]interface{} {
	return objectSchema(map[string]interface{}{
		"invoice_id": map[string]interface{}{"type": "string"},
	}, "invoice_id")
}
func (markInvoiceReviewedTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: core.RiskHigh, SideEffect: core.SideEffectIdempotent,
		Idempotency: core.IdempotencyRequired,
		OutputSchema: objectSchema(map[string]interface{}{
			"status": map[string]interface{}{"type": "string"},
		}, "status"),
	}
}
func (markInvoiceReviewedTool) Validate(input map[string]interface{}) (map[string]interface{}, error) {
	if value, ok := input["invoice_id"].(string); !ok || value == "" {
		return nil, fmt.Errorf("invoice_id is required")
	}
	return input, nil
}
func (markInvoiceReviewedTool) Execute(
	map[string]interface{},
	core.ToolContext,
) (core.ToolResult, error) {
	return core.ToolResult{}, fmt.Errorf("idempotent execution is required")
}
func (t markInvoiceReviewedTool) ExecuteIdempotent(
	input map[string]interface{},
	idempotencyKey string,
	ctx core.ToolContext,
) (core.ToolResult, error) {
	if err := ctx.Context.Err(); err != nil {
		return core.ToolResult{}, err
	}
	if idempotencyKey == "" {
		return core.ToolResult{}, fmt.Errorf("idempotency key is required")
	}
	invoiceID := input["invoice_id"].(string)
	t.ledger.mu.Lock()
	t.ledger.reviewed[invoiceID] = true
	t.ledger.mu.Unlock()
	return core.ToolResult{
		Content: map[string]interface{}{"status": "reviewed"},
		Summary: "invoice marked reviewed",
	}, nil
}
func (markInvoiceReviewedTool) Summarize(map[string]interface{}) string {
	return "mark invoice reviewed"
}

func main() {
	principal := core.Principal{TenantID: "demo-company", ActorID: "accountant@example.com"}
	ctx := core.WithPrincipal(context.Background(), principal)
	demonstrations := captureDemonstrations(ctx, principal)

	registry := tools.NewRegistry()
	ledger := &reviewLedger{reviewed: make(map[string]bool)}
	must(registry.Register(lookupPurchaseOrderTool{
		orders: map[string]map[string]interface{}{
			"PO-42": {"id": "PO-42", "total": 500.0},
		},
	}))
	must(registry.Register(markInvoiceReviewedTool{ledger: ledger}))
	catalog, err := structured.NewRegistryCatalog(structured.CatalogOptions{
		Registry: registry,
		Names:    []string{lookupToolName, markToolName},
		TrustedDescriptions: map[string]bool{
			lookupToolName: true, markToolName: true,
		},
	})
	must(err)

	document := learnedCandidateDocument(demonstrations)
	extractor, err := structured.New(structured.Options{
		Provider: fixtureProvider{document: document},
		Model:    "fixture-model",
		Catalog:  catalog,
	})
	must(err)
	workflows := workflow.NewMemoryStore()
	compiler := learning.Compiler{
		Extractor: extractor, Tools: catalog, Store: workflows,
		InputSchema: objectSchema(map[string]interface{}{
			"invoice": objectSchema(map[string]interface{}{
				"id":    map[string]interface{}{"type": "string"},
				"po_id": map[string]interface{}{"type": "string"},
				"total": map[string]interface{}{"type": "number"},
			}, "id", "po_id", "total"),
		}, "invoice"),
	}
	compilation, err := compiler.CompileMultiple(
		ctx, "invoice-reconciliation", "Invoice reconciliation",
		demonstrations,
		learning.MultiDemoOptions{
			MinimumDemonstrations: 2, MinimumSequenceConsistency: 1,
			MinimumEvidenceDemonstrations: 2,
		},
	)
	must(err)
	candidate := compilation.Candidate

	reports := evaluation.NewMemoryStore()
	suite := releaseSuite()
	report, err := evaluation.NewRunner(evaluation.RunnerOptions{Store: reports}).
		Run(ctx, suite, candidate)
	must(err)
	if !report.Gates.Passed {
		log.Fatal("deterministic release evaluation failed")
	}
	reviews := workflow.NewMemoryReviewStore()
	review, err := workflow.NewReview(
		candidate, workflow.ReviewApproved, principal,
		"verified learned mappings and high-risk approval boundary", time.Now().UTC(),
	)
	must(err)
	must(reviews.Save(ctx, review))
	publisher, err := evaluation.NewPublisher(evaluation.PublisherOptions{
		Workflows: workflows, Reports: reports, Reviews: reviews,
		ToolCatalog: catalog, RequiredSuite: suite.Name,
	})
	must(err)
	published, err := publisher.Publish(
		ctx, candidate.Workflow.ID, candidate.Version, principal,
	)
	must(err)

	routes := workflow.NewMemoryRouteStore()
	_, err = routes.Save(ctx, workflow.Route{
		TaskType: "invoice.reconcile", WorkflowID: published.Workflow.ID,
	})
	must(err)
	resolver, err := workflow.NewResolver(workflow.ResolverOptions{
		Store: workflows, RouteStore: routes,
	})
	must(err)
	resolved, err := resolver.Resolve(
		ctx, workflow.ResolutionRequest{TaskType: "invoice.reconcile"},
	)
	must(err)
	approvalAuthorization, err := policy.NewApprovalRolePolicy(
		policy.ApprovalRolePolicyOptions{
			RoleCapabilities: map[string][]string{
				"finance-approver": {"approval.grant"},
			},
			RequireDistinctApprover: true,
		},
	)
	must(err)
	approvals, err := policy.NewAuthorizedApprovalStore(
		policy.NewMemoryApprovalStore(), approvalAuthorization,
	)
	must(err)
	executions := workflow.NewMemoryExecutionStore()
	executor, err := workflow.NewExecutor(workflow.ExecutorOptions{
		Tools: workflow.RegistryRunner{Registry: registry}, Approvals: approvals,
		Executions: executions,
	})
	must(err)
	input := map[string]interface{}{"invoice": map[string]interface{}{
		"id": "INV-1001", "po_id": "PO-42", "total": 500.0,
	}}
	execution, err := executor.Execute(ctx, resolved, input, nil, principal)
	must(err)
	if execution.Status != workflow.ExecutionAwaitingApproval {
		log.Fatalf("expected approval checkpoint, got %s", execution.Status)
	}
	approver := core.Principal{
		TenantID: principal.TenantID,
		ActorID:  "controller@example.com",
		Roles:    []string{"finance-approver"},
	}
	_, err = approvals.Decide(
		core.WithPrincipal(context.Background(), approver),
		execution.PendingApprovalID, policy.ApprovalGranted, approver,
		"purchase-order total matched",
	)
	must(err)
	execution, err = executor.Resume(ctx, resolved, execution)
	must(err)
	feedbackStore := workflow.NewMemoryFeedbackStore()
	feedback, err := workflow.NewExecutionFeedback(
		execution,
		workflow.FeedbackRequest{
			Disposition: workflow.FeedbackAccepted,
			ReasonCode:  "totals.matched",
			Comment:     "The learned workflow selected the correct purchase order.",
		},
		principal, time.Now().UTC(),
	)
	must(err)
	must(feedbackStore.Save(ctx, feedback))
	feedbackAnalysis, err := learning.AnalyzeFeedback(
		[]workflow.ExecutionFeedback{feedback},
	)
	must(err)

	ledger.mu.Lock()
	reviewed := ledger.reviewed["INV-1001"]
	ledger.mu.Unlock()
	output, _ := json.MarshalIndent(map[string]interface{}{
		"demonstrations":      len(demonstrations),
		"candidate_version":   candidate.Version,
		"catalog_digest":      candidate.ToolCatalogDigest,
		"evaluation_passed":   report.Gates.Passed,
		"review_id":           review.ID,
		"published_status":    published.Status,
		"resolved_task_type":  "invoice.reconcile",
		"execution_status":    execution.Status,
		"human_approval_used": true,
		"invoice_reviewed":    reviewed,
		"feedback_id":         feedback.ID,
		"requires_new_demo":   feedbackAnalysis.RequiresNewDemonstrations,
	}, "", "  ")
	fmt.Println(string(output))
}

func captureDemonstrations(
	ctx context.Context,
	principal core.Principal,
) []observation.Demonstration {
	store := observation.NewMemoryStore()
	recorder, err := observation.NewRecorder(store)
	must(err)
	demonstrations := make([]observation.Demonstration, 0, 2)
	for index, invoiceID := range []string{"INV-1001", "INV-1002"} {
		demo, err := recorder.Start(ctx, "invoice-reconciliation", principal, nil)
		must(err)
		events := []observation.Event{
			{
				ID:     fmt.Sprintf("demo-%d-lookup", index+1),
				Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
				Application: "accounting", Action: "lookup_purchase_order",
				Input: map[string]interface{}{"po_id": fmt.Sprintf("PO-%d", index+42)},
			},
			{
				ID:     fmt.Sprintf("demo-%d-validate", index+1),
				Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
				Application: "accounting", Action: "validate_invoice_total",
				Result: map[string]interface{}{"matches": true},
			},
			{
				ID:     fmt.Sprintf("demo-%d-mark", index+1),
				Source: observation.SourceAPI, Trust: observation.TrustApplicationEvent,
				Application: "accounting", Action: "mark_invoice_reviewed",
				Input: map[string]interface{}{"invoice_id": invoiceID},
			},
		}
		for _, event := range events {
			_, err := recorder.Capture(ctx, demo.ID, event)
			must(err)
		}
		completed, err := recorder.Complete(
			ctx, demo.ID, map[string]interface{}{"status": "reviewed"},
		)
		must(err)
		demonstrations = append(demonstrations, completed)
	}
	return demonstrations
}

func learnedCandidateDocument(demonstrations []observation.Demonstration) string {
	evidence := func(eventIndex int) []map[string]interface{} {
		output := make([]map[string]interface{}, 0, len(demonstrations))
		for _, demo := range demonstrations {
			output = append(output, map[string]interface{}{
				"demonstration_id": demo.ID,
				"event_ids":        []string{demo.Trace.Events[eventIndex].ID},
			})
		}
		return output
	}
	document := map[string]interface{}{
		"description": "Compare invoice and purchase-order totals before marking the invoice reviewed.",
		"steps": []interface{}{
			map[string]interface{}{
				"id": "lookup_po", "kind": "tool", "evidence": evidence(0),
				"tool": map[string]interface{}{
					"name": lookupToolName,
					"arguments": map[string]interface{}{
						"po_id": map[string]interface{}{"ref": "input.invoice.po_id"},
					},
				},
			},
			map[string]interface{}{
				"id": "validate_total", "kind": "validation",
				"depends_on": []string{"lookup_po"}, "evidence": evidence(1),
				"validation": map[string]interface{}{
					"condition": map[string]interface{}{
						"left":     map[string]interface{}{"ref": "input.invoice.total"},
						"operator": "eq",
						"right":    map[string]interface{}{"ref": "steps.lookup_po.output.total"},
					},
					"message": "invoice total does not match purchase order",
				},
			},
			map[string]interface{}{
				"id": "mark_reviewed", "kind": "tool",
				"depends_on": []string{"validate_total"}, "evidence": evidence(2),
				"tool": map[string]interface{}{
					"name": markToolName,
					"arguments": map[string]interface{}{
						"invoice_id": map[string]interface{}{"ref": "input.invoice.id"},
					},
					"idempotency_key": map[string]interface{}{"ref": "input.invoice.id"},
				},
			},
		},
	}
	raw, err := json.Marshal(document)
	must(err)
	return string(raw)
}

func releaseSuite() evaluation.Suite {
	return evaluation.Suite{
		Name: "learned-invoice-release",
		Scenarios: []evaluation.Scenario{{
			ID: "matching-totals",
			Input: map[string]interface{}{"invoice": map[string]interface{}{
				"id": "INV-EVAL", "po_id": "PO-EVAL", "total": 500.0,
			}},
			Tools: map[string]evaluation.ToolFixture{
				lookupToolName: {
					Descriptor: lookupPurchaseOrderTool{}.ToolDescriptor(),
					Responses: []evaluation.ToolResponse{{
						Output: map[string]interface{}{"id": "PO-EVAL", "total": 500.0},
					}},
				},
				markToolName: {
					Descriptor: markInvoiceReviewedTool{}.ToolDescriptor(),
					Responses: []evaluation.ToolResponse{{
						Output: map[string]interface{}{"status": "reviewed"},
					}},
				},
			},
			Approvals: map[string]policy.ApprovalStatus{
				"mark_reviewed": policy.ApprovalGranted,
			},
			Expected: evaluation.ExpectedOutcome{
				Status: workflow.ExecutionCompleted,
				ToolCalls: []evaluation.ExpectedToolCall{
					{Name: lookupToolName, Arguments: map[string]interface{}{"po_id": "PO-EVAL"}},
					{Name: markToolName, Arguments: map[string]interface{}{"invoice_id": "INV-EVAL"}},
				},
			},
		}},
		Gates: []evaluation.Gate{
			{Metric: evaluation.MetricTaskSuccessRate, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricToolSelectionAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricParameterAccuracy, Operator: evaluation.GateAtLeast, Value: 1},
			{Metric: evaluation.MetricUnsafeActionRate, Operator: evaluation.GateAtMost, Value: 0},
			{Metric: evaluation.MetricAverageLLMCalls, Operator: evaluation.GateAtMost, Value: 0},
		},
	}
}

func objectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	return map[string]interface{}{
		"type": "object", "properties": properties,
		"required": required, "additionalProperties": false,
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
