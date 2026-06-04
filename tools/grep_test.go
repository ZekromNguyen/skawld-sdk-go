package tools

import (
	"strings"
	"testing"
)

func TestGrepToolContent(t *testing.T) {
	setupGrepFixture(t)
	defer cleanupGrepFixture()

	fb := getFallbackLines(t, "TODO", map[string]interface{}{"output_mode": "content", "-n": true})
	var actual []string
	for _, l := range fb {
		if strings.HasPrefix(l, "src/foo.ts:2:") || strings.HasPrefix(l, "src/bar.ts:3:") || strings.HasPrefix(l, "docs/readme.md:3:") {
			actual = append(actual, l)
		}
	}
	if len(actual) != 3 {
		t.Errorf("Expected 3 TODOs, found %d: %v", len(actual), fb)
	}
}
