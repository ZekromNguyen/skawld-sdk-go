package tools

import (
	"testing"
)

func TestToolsetStateDefaults(t *testing.T) {
	ts := NewToolsetState()
	for _, tset := range BuiltinToolsets {
		if !ts.IsEnabled(tset.Name) {
			t.Errorf("toolset %q should be enabled by default", tset.Name)
		}
	}
}

func TestToolsetStateDisableEnable(t *testing.T) {
	ts := NewToolsetState()

	tools := ts.Disable("terminal")
	if tools == nil {
		t.Fatal("Disable(terminal) returned nil")
	}
	if tools[0] != "Bash" {
		t.Errorf("Disable(terminal) tools = %v, want Bash first", tools)
	}
	if ts.IsEnabled("terminal") {
		t.Error("terminal should be disabled")
	}

	// Enable restores it
	ts.Enable("terminal")
	if !ts.IsEnabled("terminal") {
		t.Error("terminal should be re-enabled")
	}
}

func TestToolsetStateUnknown(t *testing.T) {
	ts := NewToolsetState()
	if result := ts.Disable("nonexistent"); result != nil {
		t.Errorf("Disable(nonexistent) = %v, want nil", result)
	}
	if result := ts.Enable("nonexistent"); result != nil {
		t.Errorf("Enable(nonexistent) = %v, want nil", result)
	}
}

func TestToolsetApplyRemovesTools(t *testing.T) {
	ts := NewToolsetState()
	ts.Disable("terminal")

	reg := DefaultTools()
	removed := ts.Apply(reg)

	found := false
	for _, name := range removed {
		if name == "Bash" {
			found = true
		}
	}
	if !found {
		t.Errorf("Apply should remove Bash, got removed = %v", removed)
	}

	if _, ok := reg.Get("Bash"); ok {
		t.Error("Bash should be unregistered after Apply")
	}

	// Other tools should remain
	if _, ok := reg.Get("Read"); !ok {
		t.Error("Read should still be registered")
	}
}

func TestToolsetEnabledDisabled(t *testing.T) {
	ts := NewToolsetState()
	ts.Disable("terminal")
	ts.Disable("web")

	enabled := ts.EnabledToolsets()
	for _, name := range enabled {
		if name == "terminal" || name == "web" {
			t.Errorf("disabled toolset %q in enabled list", name)
		}
	}

	disabled := ts.DisabledToolsets()
	foundTerminal := false
	foundWeb := false
	for _, name := range disabled {
		if name == "terminal" {
			foundTerminal = true
		}
		if name == "web" {
			foundWeb = true
		}
	}
	if !foundTerminal || !foundWeb {
		t.Errorf("disabled = %v, want terminal and web", disabled)
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := DefaultTools()

	if !r.Unregister("Bash") {
		t.Error("Unregister(Bash) should return true")
	}
	if r.Unregister("Bash") {
		t.Error("second Unregister(Bash) should return false")
	}
	if _, ok := r.Get("Bash"); ok {
		t.Error("Bash should be gone after Unregister")
	}
	if _, ok := r.Get("Read"); !ok {
		t.Error("Read should still exist")
	}
}

func TestAllToolNames(t *testing.T) {
	names := AllToolNames()
	seen := make(map[string]bool)
	for _, n := range names {
		if seen[n] {
			t.Errorf("duplicate tool name: %q", n)
		}
		seen[n] = true
	}
	if len(names) == 0 {
		t.Error("AllToolNames returned empty")
	}
}

func TestDefaultToolsAreCoveredByToolsets(t *testing.T) {
	covered := map[string]bool{}
	for _, name := range AllToolNames() {
		covered[name] = true
	}
	for _, tool := range DefaultTools().List() {
		if !covered[tool.Name()] {
			t.Errorf("default tool %q is not covered by a builtin toolset", tool.Name())
		}
	}
}
