package tools

import (
	"strings"
	"testing"
)

func TestGlobToolStarStar(t *testing.T) {
	setupFixture(t)
	defer cleanupFixture()

	tool := GlobTool{}
	input, _ := tool.Validate(map[string]interface{}{"pattern": "a/**/*.ts"})
	ctx := makeCtx(fixtureDir)
	res, _ := tool.Execute(input, ctx)

	lines := strings.Split(res.Content.(string), "\n")
	hasFoo := false
	for _, l := range lines {
		if strings.HasSuffix(l, "foo.ts") {
			hasFoo = true
		}
	}
	if !hasFoo {
		t.Errorf("expected a/foo.ts to be matched by a/**/*.ts")
	}
}
