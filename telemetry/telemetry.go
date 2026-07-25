// Package telemetry adapts safe SDK observations into vendor-neutral metric
// and completed-span records. OpenTelemetry and other backends can implement
// Sink without becoming dependencies of the core SDK.
package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type RecordKind string

const (
	RecordCounter   RecordKind = "counter"
	RecordHistogram RecordKind = "histogram"
	RecordSpan      RecordKind = "span"
)

type Record struct {
	Kind       RecordKind        `json:"kind"`
	Name       string            `json:"name"`
	Timestamp  time.Time         `json:"timestamp"`
	Value      float64           `json:"value,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Status     string            `json:"status,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Sink interface {
	Record(context.Context, Record)
}

type Observer struct {
	Sink Sink
	Now  func() time.Time
}

func (o Observer) Observe(ctx context.Context, observation core.Observation) {
	if o.Sink == nil {
		return
	}
	now := time.Now().UTC()
	if o.Now != nil {
		now = o.Now()
	}
	attributes := observationAttributes(observation)
	status := "ok"
	if observation.Error != nil || observation.ErrorKind != "" {
		status = "error"
	}
	name := operationName(observation)
	o.Sink.Record(ctx, Record{
		Kind: RecordCounter, Name: "skawld.operation.count", Timestamp: now,
		Value: 1, Unit: "operation", Status: status, Attributes: cloneAttributes(attributes),
	})
	o.Sink.Record(ctx, Record{
		Kind: RecordHistogram, Name: "skawld.operation.duration", Timestamp: now,
		Value: float64(observation.DurationMS), Unit: "ms", Status: status,
		Attributes: cloneAttributes(attributes),
	})
	if status == "error" {
		o.Sink.Record(ctx, Record{
			Kind: RecordCounter, Name: "skawld.operation.errors", Timestamp: now,
			Value: 1, Unit: "error", Status: status, Attributes: cloneAttributes(attributes),
		})
	}
	o.Sink.Record(ctx, Record{
		Kind: RecordSpan, Name: name, Timestamp: now,
		Value: float64(observation.DurationMS), Unit: "ms", Status: status,
		Attributes: attributes,
	})
}

func operationName(observation core.Observation) string {
	name := "skawld." + string(observation.Type)
	if observation.Operation != "" {
		name += "." + observation.Operation
	}
	return name
}

func observationAttributes(observation core.Observation) map[string]string {
	attributes := map[string]string{"observation.type": string(observation.Type)}
	addAttribute(attributes, "operation", observation.Operation)
	addAttribute(attributes, "session.id", observation.SessionID)
	addAttribute(attributes, "run.id", observation.RunID)
	addAttribute(attributes, "tenant.id", observation.TenantID)
	addAttribute(attributes, "actor.id", observation.ActorID)
	addAttribute(attributes, "provider.id", observation.ProviderID)
	addAttribute(attributes, "tool.name", observation.ToolName)
	if observation.Attempt > 0 {
		attributes["attempt"] = fmt.Sprint(observation.Attempt)
	}
	if observation.Retryable {
		attributes["retryable"] = "true"
	}
	if observation.ErrorKind != "" {
		attributes["error.kind"] = string(observation.ErrorKind)
	}
	return attributes
}

func addAttribute(attributes map[string]string, key, value string) {
	if value != "" {
		attributes[key] = value
	}
}

func cloneAttributes(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

// MemorySink is a concurrency-safe bounded sink for tests and local
// deployments. Oldest records are discarded when Capacity is reached.
type MemorySink struct {
	mu       sync.RWMutex
	capacity int
	records  []Record
	dropped  uint64
}

func NewMemorySink(capacity int) (*MemorySink, error) {
	if capacity < 1 {
		return nil, core.NewConfigError("telemetry memory sink capacity must be positive")
	}
	return &MemorySink{capacity: capacity, records: make([]Record, 0, capacity)}, nil
}

func (s *MemorySink) Record(ctx context.Context, record Record) {
	if s == nil || ctx.Err() != nil {
		return
	}
	record.Attributes = cloneAttributes(record.Attributes)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.records) == s.capacity {
		copy(s.records, s.records[1:])
		s.records[len(s.records)-1] = record
		s.dropped++
		return
	}
	s.records = append(s.records, record)
}

func (s *MemorySink) Snapshot() ([]Record, uint64) {
	if s == nil {
		return nil, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	output := make([]Record, len(s.records))
	for index, record := range s.records {
		record.Attributes = cloneAttributes(record.Attributes)
		output[index] = record
	}
	return output, s.dropped
}

type MultiObserver []core.Observer

func (observers MultiObserver) Observe(ctx context.Context, observation core.Observation) {
	for _, observer := range observers {
		if observer != nil {
			observer.Observe(ctx, observation)
		}
	}
}
