package evaluation

import "testing"

func TestEvaluateGatesFailsForUnmeasuredMetric(t *testing.T) {
	result, err := EvaluateGates(Metrics{}, []Gate{{
		Metric: MetricStepAccuracy, Operator: GateAtLeast, Value: 0.9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Violations) != 1 ||
		result.Violations[0].Reason != "metric was not measured by any scenario" {
		t.Fatalf("unexpected gate result: %+v", result)
	}
}

func TestEvaluateGatesRejectsUnknownMetric(t *testing.T) {
	if _, err := EvaluateGates(Metrics{}, []Gate{{
		Metric: "unknown", Operator: GateAtMost, Value: 1,
	}}); err == nil {
		t.Fatal("expected unknown metric to be rejected")
	}
}
