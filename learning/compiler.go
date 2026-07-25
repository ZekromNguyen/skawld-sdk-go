// Package learning contains the optional trace-to-workflow boundary. It does
// not execute learned output and is independent from any LLM vendor.
package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/observation"
	"github.com/ZekromNguyen/skawld-sdk-go/workflow"
)

type ExtractionRequest struct {
	WorkflowID   string
	WorkflowName string
	TenantID     string
	NextVersion  int
	// InputSchema and ContextSchema are trusted application contracts. Model
	// output must not create or broaden them.
	InputSchema    map[string]interface{}
	ContextSchema  map[string]interface{}
	Demonstrations []observation.Demonstration
	Analysis       *Analysis
}

// Extractor may use an LLM, rules, or a human-authored implementation. Its
// output is untrusted until Compiler validates and stores it as a candidate.
type Extractor interface {
	Extract(context.Context, ExtractionRequest) (workflow.Version, error)
}

// ExtractionResult carries optional provider usage without changing the
// minimal Extractor contract used by rules-based and human-authored adapters.
type ExtractionResult struct {
	Candidate workflow.Version
	Model     core.ModelID
	LLMCalls  int
	Usage     core.Usage
}

// DetailedExtractor is an optional extension implemented by model-backed
// extractors. Callers that need cost and release-gate metrics can prefer it.
type DetailedExtractor interface {
	ExtractDetailed(context.Context, ExtractionRequest) (ExtractionResult, error)
}

type ToolCatalog interface {
	Describe(context.Context, string) (core.ToolDescriptor, bool, error)
}

type Compiler struct {
	Extractor Extractor
	Tools     ToolCatalog
	Store     workflow.Store
	// InputSchema and ContextSchema are copied into every extraction request.
	// Keep them application-authored; never populate them from model output.
	InputSchema   map[string]interface{}
	ContextSchema map[string]interface{}
	Now           func() time.Time
}

type MultiDemoOptions struct {
	MinimumDemonstrations         int
	CommonActionThreshold         float64
	MinimumSequenceConsistency    float64
	MinimumEvidenceDemonstrations int
	AllowUnsubstantiatedSteps     bool
	AllowConflicts                bool
}

type CompilationResult struct {
	Candidate workflow.Version `json:"candidate"`
	Analysis  Analysis         `json:"analysis"`
	Changes   CandidateChanges `json:"changes"`
}

func (c Compiler) Compile(ctx context.Context, workflowID, workflowName string, demonstrations []observation.Demonstration) (workflow.Version, error) {
	candidate, err := c.prepare(ctx, workflowID, workflowName, demonstrations, nil)
	if err != nil {
		return workflow.Version{}, err
	}
	if _, err := validateCandidateEvidence(candidate, demonstrations, MultiDemoOptions{
		MinimumEvidenceDemonstrations: 1,
		AllowUnsubstantiatedSteps:     true,
	}); err != nil {
		return workflow.Version{}, err
	}
	return c.Store.SaveCandidate(ctx, candidate)
}

// CompileMultiple analyzes demonstrations before extraction, validates every
// evidence reference emitted by the extractor, and stores a review-only
// candidate. It never publishes learned output.
func (c Compiler) CompileMultiple(
	ctx context.Context,
	workflowID, workflowName string,
	demonstrations []observation.Demonstration,
	options MultiDemoOptions,
) (CompilationResult, error) {
	if options.MinimumDemonstrations == 0 {
		options.MinimumDemonstrations = 2
	}
	if options.MinimumEvidenceDemonstrations == 0 {
		options.MinimumEvidenceDemonstrations = 1
	}
	if options.MinimumSequenceConsistency == 0 {
		options.MinimumSequenceConsistency = 0.5
	}
	if options.MinimumDemonstrations < 2 {
		return CompilationResult{}, core.NewConfigError("multi-demonstration compilation requires at least two demonstrations")
	}
	if len(demonstrations) < options.MinimumDemonstrations {
		return CompilationResult{}, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: fmt.Sprintf("need at least %d demonstrations; received %d", options.MinimumDemonstrations, len(demonstrations)),
		}
	}
	if options.MinimumSequenceConsistency < 0 || options.MinimumSequenceConsistency > 1 {
		return CompilationResult{}, core.NewConfigError("minimum sequence consistency must be between 0 and 1")
	}
	if options.MinimumEvidenceDemonstrations < 1 {
		return CompilationResult{}, core.NewConfigError("minimum evidence demonstrations must be at least one")
	}
	analysis, err := Analyze(demonstrations, AnalyzerOptions{CommonActionThreshold: options.CommonActionThreshold})
	if err != nil {
		return CompilationResult{}, &core.SkawldError{Kind: core.ErrorValidation, Message: "analyze demonstrations", Cause: err}
	}
	if analysis.SequenceConsistency < options.MinimumSequenceConsistency {
		return CompilationResult{}, &core.SkawldError{
			Kind: core.ErrorValidation,
			Message: fmt.Sprintf(
				"demonstration sequence consistency %.3f is below required %.3f",
				analysis.SequenceConsistency, options.MinimumSequenceConsistency,
			),
		}
	}
	if len(analysis.Conflicts) > 0 && !options.AllowConflicts {
		return CompilationResult{}, &core.SkawldError{
			Kind: core.ErrorValidation,
			Message: fmt.Sprintf(
				"demonstrations contain %d ambiguous transitions; capture missing context or explicitly allow conflicts for human review",
				len(analysis.Conflicts),
			),
		}
	}
	candidate, err := c.prepare(ctx, workflowID, workflowName, demonstrations, &analysis)
	if err != nil {
		return CompilationResult{}, err
	}
	evidenceCoverage, err := validateCandidateEvidence(candidate, demonstrations, options)
	if err != nil {
		return CompilationResult{}, err
	}
	commonActions := 0
	for _, action := range analysis.Actions {
		if action.Common {
			commonActions++
		}
	}
	candidate.Learning = &workflow.LearningMetadata{
		DemonstrationCount: len(demonstrations), SequenceConsistency: analysis.SequenceConsistency,
		CommonActionCount: commonActions, ParameterCandidateCount: len(analysis.Parameters),
		StepEvidenceCoverage: evidenceCoverage, RequiresHumanReview: true,
	}
	changes := CandidateChanges{}
	versions, err := c.Store.ListVersions(ctx, workflowID)
	if err != nil {
		return CompilationResult{}, err
	}
	if len(versions) > 0 {
		changes = CompareVersions(versions[len(versions)-1], candidate)
	}
	candidate, err = c.Store.SaveCandidate(ctx, candidate)
	if err != nil {
		return CompilationResult{}, err
	}
	return CompilationResult{Candidate: candidate, Analysis: analysis, Changes: changes}, nil
}

func (c Compiler) prepare(
	ctx context.Context,
	workflowID, workflowName string,
	demonstrations []observation.Demonstration,
	analysis *Analysis,
) (workflow.Version, error) {
	if c.Extractor == nil || c.Tools == nil || c.Store == nil {
		return workflow.Version{}, core.NewConfigError("learning compiler requires extractor, tool catalog, and workflow store")
	}
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || principal.TenantID == "" {
		return workflow.Version{}, core.NewPermissionError("learning compiler requires an authenticated tenant")
	}
	if workflowID == "" || workflowName == "" || len(demonstrations) == 0 {
		return workflow.Version{}, core.NewConfigError("workflow id, name, and at least one demonstration are required")
	}
	sourceIDs := make([]string, 0, len(demonstrations))
	for _, demonstration := range demonstrations {
		if demonstration.Principal.TenantID != principal.TenantID {
			return workflow.Version{}, core.NewPermissionError("demonstration belongs to another tenant")
		}
		if err := validateLearningDemonstration(demonstration); err != nil {
			return workflow.Version{}, fmt.Errorf("demonstration %q: %w", demonstration.ID, err)
		}
		sourceIDs = append(sourceIDs, demonstration.ID)
	}
	versions, err := c.Store.ListVersions(ctx, workflowID)
	if err != nil {
		return workflow.Version{}, err
	}
	nextVersion := 1
	if len(versions) > 0 {
		nextVersion = versions[len(versions)-1].Version + 1
	}
	inputSchema, err := cloneSchema(c.InputSchema)
	if err != nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorValidation, Message: "clone trusted workflow input schema", Cause: err,
		}
	}
	contextSchema, err := cloneSchema(c.ContextSchema)
	if err != nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorValidation, Message: "clone trusted workflow context schema", Cause: err,
		}
	}
	candidate, err := c.Extractor.Extract(ctx, ExtractionRequest{
		WorkflowID: workflowID, WorkflowName: workflowName, TenantID: principal.TenantID,
		NextVersion: nextVersion, InputSchema: inputSchema,
		ContextSchema:  contextSchema,
		Demonstrations: demonstrations, Analysis: analysis,
	})
	if err != nil {
		return workflow.Version{}, fmt.Errorf("extract candidate workflow: %w", err)
	}
	candidate.SchemaVersion = workflow.SchemaVersion
	candidate.Workflow.ID = workflowID
	candidate.Workflow.Name = workflowName
	candidate.Workflow.TenantID = principal.TenantID
	candidate.Version = nextVersion
	candidate.Status = workflow.VersionCandidate
	candidate.InputSchema = inputSchema
	candidate.ContextSchema = contextSchema
	candidate.SourceDemonstrationIDs = sourceIDs
	candidate.Learning = nil
	candidate.CreatedBy = principal.ActorID
	candidate.PublishedAt = time.Time{}
	candidate.PublishedBy = ""
	now := time.Now().UTC()
	if c.Now != nil {
		now = c.Now()
	}
	candidate.CreatedAt = now
	if err := candidate.Validate(); err != nil {
		return workflow.Version{}, fmt.Errorf("validate extracted candidate: %w", err)
	}
	toolOutputs := make(map[string]map[string]interface{})
	for _, step := range candidate.Steps {
		if step.Kind != workflow.StepTool {
			continue
		}
		descriptor, exists, err := c.Tools.Describe(ctx, step.Tool.Name)
		if err != nil {
			return workflow.Version{}, err
		}
		if !exists {
			return workflow.Version{}, fmt.Errorf("extracted workflow references unknown tool %q", step.Tool.Name)
		}
		if descriptor.Risk == "" || descriptor.SideEffect == "" || descriptor.Idempotency == "" {
			return workflow.Version{}, fmt.Errorf("tool %q has incomplete safety metadata", step.Tool.Name)
		}
		toolOutputs[step.Tool.Name] = descriptor.OutputSchema
	}
	if err := workflow.ValidateReferences(candidate, toolOutputs); err != nil {
		return workflow.Version{}, &core.SkawldError{
			Kind: core.ErrorValidation, Message: "validate extracted workflow references", Cause: err,
		}
	}
	referencedTools := workflow.ReferencedToolNames(candidate)
	if len(referencedTools) > 0 {
		fingerprinter, ok := c.Tools.(workflow.ToolCatalogFingerprinter)
		if !ok {
			return workflow.Version{}, core.NewConfigError(
				"learning tool catalog must support contract fingerprinting",
			)
		}
		currentDigest, fingerprintErr := fingerprinter.ToolCatalogFingerprint(
			ctx, referencedTools,
		)
		if fingerprintErr != nil {
			return workflow.Version{}, &core.SkawldError{
				Kind: core.ErrorValidation, Message: "fingerprint learned workflow tool catalog", Cause: fingerprintErr,
			}
		}
		if candidate.ToolCatalogDigest != "" && candidate.ToolCatalogDigest != currentDigest {
			return workflow.Version{}, &core.SkawldError{
				Kind:    core.ErrorValidation,
				Message: "tool contracts changed between extraction and compilation",
			}
		}
		candidate.ToolCatalogDigest = currentDigest
	}
	return candidate, nil
}

func cloneSchema(input map[string]interface{}) (map[string]interface{}, error) {
	if input == nil {
		return nil, nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var output map[string]interface{}
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func validateCandidateEvidence(
	candidate workflow.Version,
	demonstrations []observation.Demonstration,
	options MultiDemoOptions,
) (float64, error) {
	events := make(map[string]map[string]struct{}, len(demonstrations))
	for _, demo := range demonstrations {
		events[demo.ID] = make(map[string]struct{}, len(demo.Trace.Events))
		for _, event := range demo.Trace.Events {
			events[demo.ID][event.ID] = struct{}{}
		}
	}

	requiredSteps := 0
	evidencedSteps := 0
	for _, step := range candidate.Steps {
		demonstrationEvidence := make(map[string]struct{})
		for _, evidence := range step.Evidence {
			demonstrationEvents, exists := events[evidence.DemonstrationID]
			if !exists {
				return 0, &core.SkawldError{
					Kind:    core.ErrorValidation,
					Message: fmt.Sprintf("step %q cites unknown demonstration %q", step.ID, evidence.DemonstrationID),
				}
			}
			for _, eventID := range evidence.EventIDs {
				if _, exists := demonstrationEvents[eventID]; !exists {
					return 0, &core.SkawldError{
						Kind: core.ErrorValidation,
						Message: fmt.Sprintf(
							"step %q cites unknown event %q in demonstration %q",
							step.ID, eventID, evidence.DemonstrationID,
						),
					}
				}
			}
			demonstrationEvidence[evidence.DemonstrationID] = struct{}{}
		}
		if step.Kind == workflow.StepApproval {
			continue
		}
		requiredSteps++
		if len(demonstrationEvidence) > 0 {
			evidencedSteps++
		}
		if !options.AllowUnsubstantiatedSteps && len(demonstrationEvidence) < options.MinimumEvidenceDemonstrations {
			return 0, &core.SkawldError{
				Kind: core.ErrorValidation,
				Message: fmt.Sprintf(
					"step %q has evidence from %d demonstrations; require at least %d",
					step.ID, len(demonstrationEvidence), options.MinimumEvidenceDemonstrations,
				),
			}
		}
	}
	if requiredSteps == 0 {
		return 1, nil
	}
	return float64(evidencedSteps) / float64(requiredSteps), nil
}
