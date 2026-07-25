package learning

import (
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

func TestCompareVersionsIgnoresEvidenceOnlyChanges(t *testing.T) {
	base := workflow.Version{
		Version: 1,
		Steps: []workflow.Step{{
			ID: "lookup", Kind: workflow.StepTool,
			Tool:     &workflow.ToolCall{Name: "erp.lookup"},
			Evidence: []workflow.EvidenceRef{{DemonstrationID: "demo-1", EventIDs: []string{"event-1"}}},
		}},
	}
	candidate := base
	candidate.Version = 2
	candidate.Steps = append([]workflow.Step(nil), base.Steps...)
	candidate.Steps[0].Evidence = []workflow.EvidenceRef{
		{DemonstrationID: "demo-2", EventIDs: []string{"event-2"}},
	}
	changes := CompareVersions(base, candidate)
	if len(changes.Steps) != 0 {
		t.Fatalf("evidence-only update was reported as behavior change: %+v", changes)
	}
}

func TestCompareVersionsReportsBehaviorAndOrderChanges(t *testing.T) {
	base := workflow.Version{
		Version: 3,
		Steps: []workflow.Step{
			{ID: "lookup", Kind: workflow.StepTool, Tool: &workflow.ToolCall{Name: "erp.lookup"}},
			{ID: "validate", Kind: workflow.StepValidation, Validation: &workflow.Validation{
				Condition: workflow.Condition{
					Left: workflow.Value{Literal: 1}, Operator: workflow.OpEqual, Right: workflow.Value{Literal: 1},
				},
			}},
		},
	}
	candidate := workflow.Version{
		Version: 4,
		Steps: []workflow.Step{
			{ID: "validate", Kind: workflow.StepValidation, Validation: &workflow.Validation{
				Condition: workflow.Condition{
					Left: workflow.Value{Literal: 1}, Operator: workflow.OpEqual, Right: workflow.Value{Literal: 2},
				},
			}},
			{ID: "post", Kind: workflow.StepTool, Tool: &workflow.ToolCall{Name: "erp.post"}},
		},
	}
	changes := CompareVersions(base, candidate)
	if changes.BaseVersion != 3 || len(changes.Steps) != 4 {
		t.Fatalf("unexpected changes: %+v", changes)
	}
	kinds := make(map[string]bool)
	for _, change := range changes.Steps {
		kinds[change.StepID+":"+string(change.Kind)] = true
	}
	for _, expected := range []string{
		"lookup:step_removed",
		"validate:step_modified",
		"validate:step_reordered",
		"post:step_added",
	} {
		if !kinds[expected] {
			t.Fatalf("missing %s in %+v", expected, changes.Steps)
		}
	}
}
