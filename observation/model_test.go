package observation

import (
	"testing"
	"time"
)

func TestWorkflowTraceRejectsCorrectionOfUnknownEvent(t *testing.T) {
	trace := WorkflowTrace{
		SchemaVersion: SchemaVersion,
		SessionID:     "session",
		Events: []Event{{
			SchemaVersion: SchemaVersion, ID: "correction", SessionID: "session",
			Timestamp: time.Now(), Source: SourceAPI, Trust: TrustHumanInstruction,
			Action: "correct_invoice", CorrectionOf: "missing",
		}},
	}
	if err := trace.Validate(); err == nil {
		t.Fatal("expected correction of an unknown event to be rejected")
	}
}

func TestWorkflowTraceAcceptsCorrectionOfPreviousEvent(t *testing.T) {
	now := time.Now()
	trace := WorkflowTrace{
		SchemaVersion: SchemaVersion,
		SessionID:     "session",
		Events: []Event{
			{
				SchemaVersion: SchemaVersion, ID: "original", SessionID: "session",
				Timestamp: now, Source: SourceAPI, Trust: TrustApplicationEvent, Action: "enter_amount",
			},
			{
				SchemaVersion: SchemaVersion, ID: "correction", SessionID: "session",
				Timestamp: now.Add(time.Second), Source: SourceAPI, Trust: TrustHumanInstruction,
				Action: "correct_amount", CorrectionOf: "original",
			},
		},
	}
	if err := trace.Validate(); err != nil {
		t.Fatal(err)
	}
}
