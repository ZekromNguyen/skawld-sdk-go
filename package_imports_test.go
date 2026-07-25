package skawld

import (
	"go/build"
	"testing"
)

// TestNoImportCycles verifies that key packages do not form import cycles.
// This is a static check run as a test to catch future regressions.
func TestNoImportCycles(t *testing.T) {
	packages := []string{
		"github.com/ZekromNguyen/skawld-sdk-go/core",
		"github.com/ZekromNguyen/skawld-sdk-go/internal/sse",
		"github.com/ZekromNguyen/skawld-sdk-go/internal/frontmatter",
		"github.com/ZekromNguyen/skawld-sdk-go/internal/jsoncopy",
		"github.com/ZekromNguyen/skawld-sdk-go/internal/id",
		"github.com/ZekromNguyen/skawld-sdk-go/tools",
		"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp",
		"github.com/ZekromNguyen/skawld-sdk-go/providers",
		"github.com/ZekromNguyen/skawld-sdk-go/config",
		"github.com/ZekromNguyen/skawld-sdk-go/sessions",
		"github.com/ZekromNguyen/skawld-sdk-go/skills",
		"github.com/ZekromNguyen/skawld-sdk-go/subagents",
		"github.com/ZekromNguyen/skawld-sdk-go/permissions",
		"github.com/ZekromNguyen/skawld-sdk-go/workflow",
		"github.com/ZekromNguyen/skawld-sdk-go/observation",
		"github.com/ZekromNguyen/skawld-sdk-go/learning",
		"github.com/ZekromNguyen/skawld-sdk-go/learning/structured",
		"github.com/ZekromNguyen/skawld-sdk-go/automation",
	}
	for _, pkg := range packages {
		_, err := build.Import(pkg, "", build.FindOnly)
		if err != nil {
			t.Errorf("failed to import %s: %v", pkg, err)
		}
	}
}
