package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestObserverProducesSafeMetricsAndCompletedSpan(t *testing.T) {
	sink, err := NewMemorySink(10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	Observer{Sink: sink, Now: func() time.Time { return now }}.Observe(context.Background(), core.Observation{
		Type: core.ObservationToolExecution, Operation: "execute",
		SessionID: "session", RunID: "run", TenantID: "tenant", ActorID: "actor",
		ToolName: "erp.lookup", DurationMS: 12, ErrorKind: core.ErrorToolExecution,
		Error: errors.New("secret customer value"),
	})
	records, dropped := sink.Snapshot()
	if dropped != 0 || len(records) != 4 {
		t.Fatalf("records=%d dropped=%d", len(records), dropped)
	}
	if records[3].Kind != RecordSpan || records[3].Name != "skawld.tool_execution.execute" ||
		records[3].Status != "error" || records[3].Attributes["run.id"] != "run" {
		t.Fatalf("unexpected span record: %+v", records[3])
	}
	for _, record := range records {
		for _, value := range record.Attributes {
			if value == "secret customer value" {
				t.Fatal("telemetry leaked raw error content")
			}
		}
	}
}

func TestMemorySinkIsBoundedAndConcurrent(t *testing.T) {
	sink, err := NewMemorySink(8)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := 0; index < 20; index++ {
				sink.Record(context.Background(), Record{Kind: RecordCounter, Name: "test", Value: 1})
			}
		}()
	}
	workers.Wait()
	records, dropped := sink.Snapshot()
	if len(records) != 8 || dropped != 152 {
		t.Fatalf("records=%d dropped=%d", len(records), dropped)
	}
}
