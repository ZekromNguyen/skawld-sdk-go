package tools

import (
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type fingerprintTool struct {
	name   string
	schema map[string]interface{}
	risk   core.RiskLevel
}

func (t fingerprintTool) Name() string                        { return t.name }
func (t fingerprintTool) Description() string                 { return "contract for " + t.name }
func (t fingerprintTool) InputSchema() map[string]interface{} { return t.schema }
func (t fingerprintTool) Scope() core.ToolScope               { return core.ToolScopeRead }
func (t fingerprintTool) ParallelSafe() bool                  { return true }
func (t fingerprintTool) Validate(input map[string]interface{}) (map[string]interface{}, error) {
	return input, nil
}
func (t fingerprintTool) Execute(map[string]interface{}, core.ToolContext) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
func (t fingerprintTool) Summarize(map[string]interface{}) string { return t.name }
func (t fingerprintTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk: t.risk, SideEffect: core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
	}
}

func TestCatalogFingerprintIsStableAndDetectsContractChanges(t *testing.T) {
	first := NewRegistry()
	for _, tool := range []core.Tool{
		fingerprintTool{name: "b", risk: core.RiskLow, schema: map[string]interface{}{"type": "object"}},
		fingerprintTool{name: "a", risk: core.RiskLow, schema: map[string]interface{}{"type": "object"}},
	} {
		if err := first.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	one, err := CatalogFingerprint(first, []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := CatalogFingerprint(first, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("fingerprint depends on caller ordering: %q != %q", one, two)
	}

	changed := NewRegistry()
	if err := changed.Register(fingerprintTool{
		name: "a", risk: core.RiskMedium,
		schema: map[string]interface{}{"type": "object"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := changed.Register(fingerprintTool{
		name: "b", risk: core.RiskLow,
		schema: map[string]interface{}{"type": "object"},
	}); err != nil {
		t.Fatal(err)
	}
	three, err := CatalogFingerprint(changed, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if one == three {
		t.Fatal("safety metadata change did not alter catalog fingerprint")
	}
}

func TestCatalogFingerprintRequiresExplicitSelection(t *testing.T) {
	if _, err := CatalogFingerprint(NewRegistry(), nil); err == nil {
		t.Fatal("expected empty catalog selection to fail")
	}
	if _, err := CatalogFingerprint(NewRegistry(), []string{"missing"}); err == nil {
		t.Fatal("expected missing selected tool to fail")
	}
}
