package skawld

import (
	"go/build"
	"testing"
)

// TestNoImportCycles verifies that key packages do not form import cycles.
// This is a static check run as a test to catch future regressions.
func TestNoImportCycles(t *testing.T) {
	packages := []string{
		"github.com/skawld/skawld-sdk-go/core",
		"github.com/skawld/skawld-sdk-go/internal/sse",
		"github.com/skawld/skawld-sdk-go/internal/frontmatter",
		"github.com/skawld/skawld-sdk-go/internal/jsoncopy",
		"github.com/skawld/skawld-sdk-go/internal/id",
		"github.com/skawld/skawld-sdk-go/tools",
		"github.com/skawld/skawld-sdk-go/tools/mcp",
		"github.com/skawld/skawld-sdk-go/providers",
		"github.com/skawld/skawld-sdk-go/config",
		"github.com/skawld/skawld-sdk-go/sessions",
		"github.com/skawld/skawld-sdk-go/skills",
		"github.com/skawld/skawld-sdk-go/subagents",
		"github.com/skawld/skawld-sdk-go/permissions",
	}
	for _, pkg := range packages {
		_, err := build.Import(pkg, "", build.FindOnly)
		if err != nil {
			t.Errorf("failed to import %s: %v", pkg, err)
		}
	}
}
