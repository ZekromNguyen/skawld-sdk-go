package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToolAbsolutePattern(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	absPattern := filepath.ToSlash(filepath.Join(fixtureDir, "a", "*.ts"))
	input, _ := tool.Validate(map[string]interface{}{"pattern": absPattern})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	if strings.Contains(res.Content.(string), "No matches found") {
		t.Errorf("expected matches, got no matches found for %s", absPattern)
	}
}
