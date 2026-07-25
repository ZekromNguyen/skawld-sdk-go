package learning

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type ChangeKind string

const (
	ChangeStepAdded     ChangeKind = "step_added"
	ChangeStepRemoved   ChangeKind = "step_removed"
	ChangeStepModified  ChangeKind = "step_modified"
	ChangeStepReordered ChangeKind = "step_reordered"
)

// CandidateChanges describes executable differences from the latest stored
// version. Evidence-only changes are intentionally excluded from behavior
// changes but remain available on each step for review.
type CandidateChanges struct {
	BaseVersion          int          `json:"base_version,omitempty"`
	InputSchemaChanged   bool         `json:"input_schema_changed"`
	ContextSchemaChanged bool         `json:"context_schema_changed"`
	Steps                []StepChange `json:"steps,omitempty"`
}

type StepChange struct {
	StepID            string     `json:"step_id"`
	Kind              ChangeKind `json:"kind"`
	PreviousPosition  int        `json:"previous_position,omitempty"`
	CandidatePosition int        `json:"candidate_position,omitempty"`
	PreviousTool      string     `json:"previous_tool,omitempty"`
	CandidateTool     string     `json:"candidate_tool,omitempty"`
}

// CompareVersions returns a deterministic, behavior-focused change set.
func CompareVersions(base, candidate workflow.Version) CandidateChanges {
	changes := CandidateChanges{
		BaseVersion:          base.Version,
		InputSchemaChanged:   !reflect.DeepEqual(base.InputSchema, candidate.InputSchema),
		ContextSchemaChanged: !reflect.DeepEqual(base.ContextSchema, candidate.ContextSchema),
	}
	type indexedStep struct {
		position int
		step     workflow.Step
	}
	baseSteps := make(map[string]indexedStep, len(base.Steps))
	candidateSteps := make(map[string]indexedStep, len(candidate.Steps))
	for position, step := range base.Steps {
		baseSteps[step.ID] = indexedStep{position: position, step: step}
	}
	for position, step := range candidate.Steps {
		candidateSteps[step.ID] = indexedStep{position: position, step: step}
	}
	for stepID, baseStep := range baseSteps {
		candidateStep, exists := candidateSteps[stepID]
		if !exists {
			changes.Steps = append(changes.Steps, StepChange{
				StepID: stepID, Kind: ChangeStepRemoved,
				PreviousPosition: baseStep.position + 1, PreviousTool: toolName(baseStep.step),
			})
			continue
		}
		if stepBehaviorFingerprint(baseStep.step) != stepBehaviorFingerprint(candidateStep.step) {
			changes.Steps = append(changes.Steps, StepChange{
				StepID: stepID, Kind: ChangeStepModified,
				PreviousPosition: baseStep.position + 1, CandidatePosition: candidateStep.position + 1,
				PreviousTool: toolName(baseStep.step), CandidateTool: toolName(candidateStep.step),
			})
		}
		if baseStep.position != candidateStep.position {
			changes.Steps = append(changes.Steps, StepChange{
				StepID: stepID, Kind: ChangeStepReordered,
				PreviousPosition: baseStep.position + 1, CandidatePosition: candidateStep.position + 1,
			})
		}
	}
	for stepID, candidateStep := range candidateSteps {
		if _, exists := baseSteps[stepID]; exists {
			continue
		}
		changes.Steps = append(changes.Steps, StepChange{
			StepID: stepID, Kind: ChangeStepAdded,
			CandidatePosition: candidateStep.position + 1, CandidateTool: toolName(candidateStep.step),
		})
	}
	sort.Slice(changes.Steps, func(i, j int) bool {
		if changes.Steps[i].StepID == changes.Steps[j].StepID {
			return changes.Steps[i].Kind < changes.Steps[j].Kind
		}
		return changes.Steps[i].StepID < changes.Steps[j].StepID
	})
	return changes
}

func stepBehaviorFingerprint(step workflow.Step) string {
	step.Evidence = nil
	raw, _ := json.Marshal(step)
	return string(raw)
}

func toolName(step workflow.Step) string {
	if step.Tool == nil {
		return ""
	}
	return step.Tool.Name
}
