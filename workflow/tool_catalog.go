package workflow

import (
	"context"
	"sort"
)

// ToolCatalogFingerprinter is an optional safety extension implemented by
// tool catalogs and runners that can attest to their current contracts.
type ToolCatalogFingerprinter interface {
	ToolCatalogFingerprint(context.Context, []string) (string, error)
}

// ReferencedToolNames returns the unique tool names used by a workflow in
// stable order.
func ReferencedToolNames(version Version) []string {
	seen := make(map[string]struct{})
	for _, step := range version.Steps {
		if step.Tool != nil && step.Tool.Name != "" {
			seen[step.Tool.Name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
