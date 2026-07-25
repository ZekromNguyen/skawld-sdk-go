package learning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/observation"
)

const defaultCommonActionThreshold = 2.0 / 3.0

// AnalyzerOptions controls deterministic trace comparison. The analyzer does
// not infer executable conditions or values; it only describes evidence that
// an extractor and human reviewer can evaluate.
type AnalyzerOptions struct {
	CommonActionThreshold float64
}

type Analysis struct {
	DemonstrationIDs      []string             `json:"demonstration_ids"`
	Actions               []ActionPattern      `json:"actions"`
	Parameters            []ParameterCandidate `json:"parameters,omitempty"`
	BranchCandidates      []BranchCandidate    `json:"branch_candidates,omitempty"`
	SequenceVariants      []SequenceVariant    `json:"sequence_variants"`
	Conflicts             []Conflict           `json:"conflicts,omitempty"`
	Findings              []Finding            `json:"findings,omitempty"`
	SequenceConsistency   float64              `json:"sequence_consistency"`
	CommonActionThreshold float64              `json:"common_action_threshold"`
}

// BranchCandidate identifies an observed field that perfectly separates two
// or more next-action outcomes for the same semantic action. It exposes field
// paths and evidence identities, but never observed values.
type BranchCandidate struct {
	Action               ActionSignature  `json:"action"`
	Location             string           `json:"location"`
	Path                 string           `json:"path"`
	OutcomeCount         int              `json:"outcome_count"`
	DistinctValueCount   int              `json:"distinct_value_count"`
	Occurrences          int              `json:"occurrences"`
	DemonstrationSupport float64          `json:"demonstration_support"`
	DemonstrationIDs     []string         `json:"demonstration_ids"`
	Evidence             []BranchEvidence `json:"evidence"`
}

type BranchEvidence struct {
	DemonstrationID string          `json:"demonstration_id"`
	EventID         string          `json:"event_id"`
	NextAction      ActionSignature `json:"next_action"`
}

type ActionSignature struct {
	Application string `json:"application,omitempty"`
	Action      string `json:"action"`
	EntityType  string `json:"entity_type,omitempty"`
}

type ActionPattern struct {
	Signature            ActionSignature `json:"signature"`
	DemonstrationIDs     []string        `json:"demonstration_ids"`
	DemonstrationSupport float64         `json:"demonstration_support"`
	Occurrences          int             `json:"occurrences"`
	MeanPosition         float64         `json:"mean_position"`
	Common               bool            `json:"common"`
}

type ParameterClassification string

const (
	ParameterConstant ParameterClassification = "constant"
	ParameterVariable ParameterClassification = "variable"
)

// ParameterCandidate reports value stability without retaining observed
// values. This avoids copying potentially sensitive business data into
// learning reports.
type ParameterCandidate struct {
	Action               ActionSignature         `json:"action"`
	Location             string                  `json:"location"`
	Path                 string                  `json:"path"`
	Classification       ParameterClassification `json:"classification"`
	Optional             bool                    `json:"optional"`
	DemonstrationSupport float64                 `json:"demonstration_support"`
	DistinctValueCount   int                     `json:"distinct_value_count"`
}

type SequenceVariant struct {
	Fingerprint      string            `json:"fingerprint"`
	DemonstrationIDs []string          `json:"demonstration_ids"`
	Actions          []ActionSignature `json:"actions"`
}

// Conflict identifies demonstrations that take different next actions from
// the same observed semantic action, inputs, context, and recorded decision.
// It is a review signal, not proof that either demonstration is wrong.
type Conflict struct {
	Kind             string          `json:"kind"`
	Action           ActionSignature `json:"action"`
	DemonstrationIDs []string        `json:"demonstration_ids"`
	EventIDs         []string        `json:"event_ids"`
	OutcomeCount     int             `json:"outcome_count"`
}

type FindingSeverity string

const (
	FindingInfo    FindingSeverity = "info"
	FindingWarning FindingSeverity = "warning"
)

type Finding struct {
	Code             string          `json:"code"`
	Severity         FindingSeverity `json:"severity"`
	Message          string          `json:"message"`
	DemonstrationIDs []string        `json:"demonstration_ids,omitempty"`
	EventIDs         []string        `json:"event_ids,omitempty"`
}

type actionStats struct {
	signature ActionSignature
	demos     map[string]struct{}
	positions []float64
	count     int
}

type fieldKey struct {
	signature ActionSignature
	location  string
	path      string
}

type fieldStats struct {
	demos  map[string]struct{}
	values map[string]struct{}
}

type transitionKey struct {
	signature ActionSignature
	state     string
}

type transitionStats struct {
	outcomes map[string]struct{}
	demos    map[string]struct{}
	events   map[string]struct{}
}

type branchObservation struct {
	demonstrationID string
	eventID         string
	next            ActionSignature
	nextEnd         bool
	fields          map[string]string
}

// Analyze compares completed demonstrations using semantic action identities,
// relative order, and redacted field fingerprints. It deliberately stops
// short of inventing workflow conditions or argument mappings.
func Analyze(demonstrations []observation.Demonstration, options AnalyzerOptions) (Analysis, error) {
	threshold := options.CommonActionThreshold
	if threshold == 0 {
		threshold = defaultCommonActionThreshold
	}
	if threshold < 0 || threshold > 1 {
		return Analysis{}, fmt.Errorf("common action threshold must be between 0 and 1")
	}
	if len(demonstrations) == 0 {
		return Analysis{}, fmt.Errorf("at least one demonstration is required")
	}

	analysis := Analysis{CommonActionThreshold: threshold}
	actionValues := make(map[ActionSignature]*actionStats)
	fieldValues := make(map[fieldKey]*fieldStats)
	transitions := make(map[transitionKey]*transitionStats)
	branchObservations := make(map[ActionSignature][]branchObservation)
	sequences := make([][]ActionSignature, 0, len(demonstrations))
	sequenceGroups := make(map[string]*SequenceVariant)
	seenDemonstrations := make(map[string]struct{}, len(demonstrations))
	workflowKey := demonstrations[0].WorkflowKey
	tenantID := demonstrations[0].Principal.TenantID

	for _, demo := range demonstrations {
		if demo.ID == "" {
			return Analysis{}, fmt.Errorf("demonstration id is required")
		}
		if _, exists := seenDemonstrations[demo.ID]; exists {
			return Analysis{}, fmt.Errorf("duplicate demonstration %q", demo.ID)
		}
		seenDemonstrations[demo.ID] = struct{}{}
		if demo.WorkflowKey != workflowKey {
			return Analysis{}, fmt.Errorf("demonstration %q has workflow key %q; expected %q", demo.ID, demo.WorkflowKey, workflowKey)
		}
		if demo.Principal.TenantID != tenantID {
			return Analysis{}, fmt.Errorf("demonstration %q belongs to another tenant", demo.ID)
		}
		if err := validateLearningDemonstration(demo); err != nil {
			return Analysis{}, fmt.Errorf("demonstration %q: %w", demo.ID, err)
		}
		if len(demo.Trace.Events) == 0 {
			return Analysis{}, fmt.Errorf("demonstration %q contains no events", demo.ID)
		}

		analysis.DemonstrationIDs = append(analysis.DemonstrationIDs, demo.ID)
		sequence := make([]ActionSignature, 0, len(demo.Trace.Events))
		for index, event := range demo.Trace.Events {
			signature := signatureFor(event)
			sequence = append(sequence, signature)
			stats := actionValues[signature]
			if stats == nil {
				stats = &actionStats{signature: signature, demos: make(map[string]struct{})}
				actionValues[signature] = stats
			}
			stats.demos[demo.ID] = struct{}{}
			stats.count++
			denominator := len(demo.Trace.Events) - 1
			position := 0.0
			if denominator > 0 {
				position = float64(index) / float64(denominator)
			}
			stats.positions = append(stats.positions, position)
			collectEventFields(fieldValues, signature, demo.ID, event)
			next := "<end>"
			nextSignature := ActionSignature{}
			nextEnd := true
			if index+1 < len(demo.Trace.Events) {
				nextSignature = signatureFor(demo.Trace.Events[index+1])
				next = signatureKey(nextSignature)
				nextEnd = false
			}
			branchObservations[signature] = append(
				branchObservations[signature],
				newBranchObservation(
					demo.ID, event, nextSignature, nextEnd,
				),
			)
			transition := transitionKey{
				signature: signature,
				state:     valueFingerprint([]interface{}{event.Input, event.Context, event.Decision}),
			}
			transitionValue := transitions[transition]
			if transitionValue == nil {
				transitionValue = &transitionStats{
					outcomes: make(map[string]struct{}),
					demos:    make(map[string]struct{}),
					events:   make(map[string]struct{}),
				}
				transitions[transition] = transitionValue
			}
			transitionValue.outcomes[next] = struct{}{}
			transitionValue.demos[demo.ID] = struct{}{}
			transitionValue.events[event.ID] = struct{}{}

			if event.Error != "" {
				analysis.Findings = append(analysis.Findings, Finding{
					Code: "observed_error", Severity: FindingWarning,
					Message:          "a demonstration contains an errored action",
					DemonstrationIDs: []string{demo.ID}, EventIDs: []string{event.ID},
				})
			}
			if event.CorrectionOf != "" {
				analysis.Findings = append(analysis.Findings, Finding{
					Code: "observed_correction", Severity: FindingWarning,
					Message:          "a demonstration contains a human correction",
					DemonstrationIDs: []string{demo.ID}, EventIDs: []string{event.ID, event.CorrectionOf},
				})
			}
		}
		sequences = append(sequences, sequence)
		fingerprint := sequenceFingerprint(sequence)
		variant := sequenceGroups[fingerprint]
		if variant == nil {
			variant = &SequenceVariant{Fingerprint: fingerprint, Actions: append([]ActionSignature(nil), sequence...)}
			sequenceGroups[fingerprint] = variant
		}
		variant.DemonstrationIDs = append(variant.DemonstrationIDs, demo.ID)
	}

	sort.Strings(analysis.DemonstrationIDs)
	demoCount := float64(len(demonstrations))
	for _, stats := range actionValues {
		support := float64(len(stats.demos)) / demoCount
		analysis.Actions = append(analysis.Actions, ActionPattern{
			Signature: stats.signature, DemonstrationIDs: sortedSet(stats.demos),
			DemonstrationSupport: support, Occurrences: stats.count,
			MeanPosition: mean(stats.positions), Common: support >= threshold,
		})
	}
	sort.Slice(analysis.Actions, func(i, j int) bool {
		return signatureKey(analysis.Actions[i].Signature) < signatureKey(analysis.Actions[j].Signature)
	})

	for key, stats := range fieldValues {
		support := float64(len(stats.demos)) / demoCount
		classification := ParameterConstant
		if len(stats.values) > 1 {
			classification = ParameterVariable
		}
		analysis.Parameters = append(analysis.Parameters, ParameterCandidate{
			Action: key.signature, Location: key.location, Path: key.path,
			Classification: classification, Optional: support < 1,
			DemonstrationSupport: support, DistinctValueCount: len(stats.values),
		})
	}
	sort.Slice(analysis.Parameters, func(i, j int) bool {
		left := signatureKey(analysis.Parameters[i].Action) + "\x00" + analysis.Parameters[i].Location + "\x00" + analysis.Parameters[i].Path
		right := signatureKey(analysis.Parameters[j].Action) + "\x00" + analysis.Parameters[j].Location + "\x00" + analysis.Parameters[j].Path
		return left < right
	})

	for _, variant := range sequenceGroups {
		sort.Strings(variant.DemonstrationIDs)
		analysis.SequenceVariants = append(analysis.SequenceVariants, *variant)
	}
	sort.Slice(analysis.SequenceVariants, func(i, j int) bool {
		return analysis.SequenceVariants[i].Fingerprint < analysis.SequenceVariants[j].Fingerprint
	})
	analysis.SequenceConsistency = sequenceConsistency(sequences)
	analysis.BranchCandidates = discoverBranchCandidates(
		branchObservations, len(demonstrations),
	)
	for transition, stats := range transitions {
		if len(stats.outcomes) < 2 {
			continue
		}
		conflict := Conflict{
			Kind: "ambiguous_transition", Action: transition.signature,
			DemonstrationIDs: sortedSet(stats.demos), EventIDs: sortedSet(stats.events),
			OutcomeCount: len(stats.outcomes),
		}
		analysis.Conflicts = append(analysis.Conflicts, conflict)
		analysis.Findings = append(analysis.Findings, Finding{
			Code: "ambiguous_transition", Severity: FindingWarning,
			Message:          "matching observed state led to different next actions; capture more context or request human review",
			DemonstrationIDs: append([]string(nil), conflict.DemonstrationIDs...),
			EventIDs:         append([]string(nil), conflict.EventIDs...),
		})
	}
	sort.Slice(analysis.Conflicts, func(i, j int) bool {
		return signatureKey(analysis.Conflicts[i].Action) < signatureKey(analysis.Conflicts[j].Action)
	})
	if len(analysis.SequenceVariants) > 1 {
		analysis.Findings = append(analysis.Findings, Finding{
			Code: "sequence_variation", Severity: FindingInfo,
			Message:          "demonstrations contain different action sequences; review for conditions, optional steps, or noise",
			DemonstrationIDs: append([]string(nil), analysis.DemonstrationIDs...),
		})
	}
	for _, action := range analysis.Actions {
		if !action.Common {
			analysis.Findings = append(analysis.Findings, Finding{
				Code: "low_support_action", Severity: FindingInfo,
				Message:          fmt.Sprintf("action %q appears in only %.0f%% of demonstrations", action.Signature.Action, action.DemonstrationSupport*100),
				DemonstrationIDs: append([]string(nil), action.DemonstrationIDs...),
			})
		}
	}
	sort.SliceStable(analysis.Findings, func(i, j int) bool {
		if analysis.Findings[i].Code == analysis.Findings[j].Code {
			return strings.Join(analysis.Findings[i].EventIDs, "\x00") < strings.Join(analysis.Findings[j].EventIDs, "\x00")
		}
		return analysis.Findings[i].Code < analysis.Findings[j].Code
	})
	return analysis, nil
}

func newBranchObservation(
	demonstrationID string,
	event observation.Event,
	next ActionSignature,
	nextEnd bool,
) branchObservation {
	fields := make(map[string]string)
	locations := []struct {
		name  string
		value map[string]interface{}
	}{
		{name: "input", value: event.Input},
		{name: "output", value: event.Output},
		{name: "context", value: event.Context},
		{name: "decision", value: event.Decision},
		{name: "result", value: event.Result},
	}
	for _, location := range locations {
		flattenFields(location.value, "", func(path string, value interface{}) {
			fields[location.name+"\x00"+path] = valueFingerprint(value)
		})
	}
	return branchObservation{
		demonstrationID: demonstrationID, eventID: event.ID,
		next: next, nextEnd: nextEnd, fields: fields,
	}
}

func discoverBranchCandidates(
	observations map[ActionSignature][]branchObservation,
	demonstrationCount int,
) []BranchCandidate {
	output := make([]BranchCandidate, 0)
	for action, items := range observations {
		outcomes := make(map[string]struct{})
		paths := make(map[string]struct{})
		demonstrations := make(map[string]struct{})
		for _, item := range items {
			outcomes[branchOutcomeKey(item)] = struct{}{}
			demonstrations[item.demonstrationID] = struct{}{}
			for path := range item.fields {
				paths[path] = struct{}{}
			}
		}
		if len(outcomes) < 2 {
			continue
		}
		for combinedPath := range paths {
			valueOutcomes := make(map[string]map[string]struct{})
			for _, item := range items {
				value, exists := item.fields[combinedPath]
				if !exists {
					value = "<missing>"
				}
				if valueOutcomes[value] == nil {
					valueOutcomes[value] = make(map[string]struct{})
				}
				valueOutcomes[value][branchOutcomeKey(item)] = struct{}{}
			}
			discriminates := len(valueOutcomes) >= 2
			coveredOutcomes := make(map[string]struct{})
			for _, valueOutcome := range valueOutcomes {
				if len(valueOutcome) != 1 {
					discriminates = false
					break
				}
				for outcome := range valueOutcome {
					coveredOutcomes[outcome] = struct{}{}
				}
			}
			if !discriminates || len(coveredOutcomes) != len(outcomes) {
				continue
			}
			parts := strings.SplitN(combinedPath, "\x00", 2)
			candidate := BranchCandidate{
				Action: action, Location: parts[0], Path: parts[1],
				OutcomeCount:       len(outcomes),
				DistinctValueCount: len(valueOutcomes),
				Occurrences:        len(items),
				DemonstrationIDs:   sortedSet(demonstrations),
			}
			if demonstrationCount > 0 {
				candidate.DemonstrationSupport =
					float64(len(demonstrations)) /
						float64(demonstrationCount)
			}
			for _, item := range items {
				next := item.next
				if item.nextEnd {
					next = ActionSignature{Action: "<end>"}
				}
				candidate.Evidence = append(
					candidate.Evidence, BranchEvidence{
						DemonstrationID: item.demonstrationID,
						EventID:         item.eventID, NextAction: next,
					},
				)
			}
			sort.Slice(candidate.Evidence, func(i, j int) bool {
				left := candidate.Evidence[i]
				right := candidate.Evidence[j]
				if left.DemonstrationID == right.DemonstrationID {
					return left.EventID < right.EventID
				}
				return left.DemonstrationID < right.DemonstrationID
			})
			output = append(output, candidate)
		}
	}
	sort.Slice(output, func(i, j int) bool {
		left := signatureKey(output[i].Action) + "\x00" +
			output[i].Location + "\x00" + output[i].Path
		right := signatureKey(output[j].Action) + "\x00" +
			output[j].Location + "\x00" + output[j].Path
		return left < right
	})
	return output
}

func branchOutcomeKey(observation branchObservation) string {
	if observation.nextEnd {
		return "<end>"
	}
	return signatureKey(observation.next)
}

func signatureFor(event observation.Event) ActionSignature {
	entityType := ""
	if event.Entity != nil {
		entityType = event.Entity.Type
	}
	return ActionSignature{Application: event.Application, Action: event.Action, EntityType: entityType}
}

func signatureKey(signature ActionSignature) string {
	return signature.Application + "\x00" + signature.Action + "\x00" + signature.EntityType
}

func sequenceFingerprint(sequence []ActionSignature) string {
	raw, _ := json.Marshal(sequence)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sequenceConsistency(sequences [][]ActionSignature) float64 {
	if len(sequences) < 2 {
		return 1
	}
	total := 0.0
	pairs := 0
	for left := 0; left < len(sequences); left++ {
		for right := left + 1; right < len(sequences); right++ {
			maxLength := len(sequences[left])
			if len(sequences[right]) > maxLength {
				maxLength = len(sequences[right])
			}
			similarity := 1.0
			if maxLength > 0 {
				similarity = float64(longestCommonSubsequence(sequences[left], sequences[right])) / float64(maxLength)
			}
			total += similarity
			pairs++
		}
	}
	return total / float64(pairs)
}

func longestCommonSubsequence(left, right []ActionSignature) int {
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for _, leftValue := range left {
		for rightIndex, rightValue := range right {
			if leftValue == rightValue {
				current[rightIndex+1] = previous[rightIndex] + 1
			} else if previous[rightIndex+1] > current[rightIndex] {
				current[rightIndex+1] = previous[rightIndex+1]
			} else {
				current[rightIndex+1] = current[rightIndex]
			}
		}
		previous, current = current, previous
		clear(current)
	}
	return previous[len(right)]
}

func collectEventFields(values map[fieldKey]*fieldStats, signature ActionSignature, demoID string, event observation.Event) {
	locations := []struct {
		name  string
		value map[string]interface{}
	}{
		{name: "input", value: event.Input},
		{name: "output", value: event.Output},
		{name: "context", value: event.Context},
		{name: "decision", value: event.Decision},
		{name: "result", value: event.Result},
	}
	for _, location := range locations {
		flattenFields(location.value, "", func(path string, value interface{}) {
			key := fieldKey{signature: signature, location: location.name, path: path}
			stats := values[key]
			if stats == nil {
				stats = &fieldStats{demos: make(map[string]struct{}), values: make(map[string]struct{})}
				values[key] = stats
			}
			stats.demos[demoID] = struct{}{}
			stats.values[valueFingerprint(value)] = struct{}{}
		})
	}
}

func flattenFields(value map[string]interface{}, prefix string, visit func(string, interface{})) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		nested, ok := value[key].(map[string]interface{})
		if ok {
			flattenFields(nested, path, visit)
			continue
		}
		visit(path, value[key])
	}
}

func valueFingerprint(value interface{}) string {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(fmt.Sprintf("%T", value))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sortedSet(values map[string]struct{}) []string {
	output := make([]string, 0, len(values))
	for value := range values {
		output = append(output, value)
	}
	sort.Strings(output)
	return output
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func validateLearningDemonstration(demo observation.Demonstration) error {
	if demo.Status != observation.DemonstrationCompleted {
		return fmt.Errorf("is not completed")
	}
	if err := demo.Trace.Validate(); err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	for _, event := range demo.Trace.Events {
		if event.Principal.TenantID != demo.Principal.TenantID {
			return fmt.Errorf("event %q belongs to another tenant", event.ID)
		}
	}
	return nil
}
