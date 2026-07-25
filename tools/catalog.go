package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

// CatalogFingerprint returns a stable digest of the explicitly selected tool
// contracts. It includes model-visible schemas and runtime safety metadata so
// learned workflows can fail closed when a registered capability changes.
//
// Callers must supply an allowlist. Fingerprinting every registered tool would
// make unrelated registrations invalidate otherwise independent workflows.
func CatalogFingerprint(registry *Registry, names []string) (string, error) {
	if registry == nil {
		return "", fmt.Errorf("tool registry is nil")
	}
	selected, err := normalizedCatalogNames(names)
	if err != nil {
		return "", err
	}
	type catalogEntry struct {
		Name            string                 `json:"name"`
		Description     string                 `json:"description"`
		InputSchema     map[string]interface{} `json:"input_schema,omitempty"`
		OutputSchema    map[string]interface{} `json:"output_schema,omitempty"`
		Risk            string                 `json:"risk"`
		SideEffect      string                 `json:"side_effect"`
		Idempotency     string                 `json:"idempotency"`
		TimeoutNanos    int64                  `json:"timeout_nanos"`
		Permissions     []string               `json:"permissions,omitempty"`
		NetworkAccess   bool                   `json:"network_access"`
		HandlesSecrets  bool                   `json:"handles_secrets"`
		UntrustedOutput bool                   `json:"contains_untrusted_output"`
		ParallelSafe    bool                   `json:"parallel_safe"`
	}
	entries := make([]catalogEntry, 0, len(selected))
	for _, name := range selected {
		tool, exists := registry.Get(name)
		if !exists {
			return "", fmt.Errorf("tool %q is not registered", name)
		}
		descriptor := core.DescribeTool(tool)
		permissions := append([]string(nil), descriptor.Permissions...)
		sort.Strings(permissions)
		entries = append(entries, catalogEntry{
			Name: name, Description: tool.Description(),
			InputSchema: tool.InputSchema(), OutputSchema: descriptor.OutputSchema,
			Risk: string(descriptor.Risk), SideEffect: string(descriptor.SideEffect),
			Idempotency: string(descriptor.Idempotency), TimeoutNanos: int64(descriptor.Timeout),
			Permissions: permissions, NetworkAccess: descriptor.NetworkAccess,
			HandlesSecrets:  descriptor.HandlesSecrets,
			UntrustedOutput: descriptor.ContainsUntrusted,
			ParallelSafe:    tool.ParallelSafe(),
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("serialize tool catalog: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func normalizedCatalogNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("tool catalog requires at least one explicitly selected tool")
	}
	selected := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("tool catalog contains an empty tool name")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("tool catalog contains duplicate tool %q", name)
		}
		seen[name] = struct{}{}
		selected = append(selected, name)
	}
	sort.Strings(selected)
	return selected, nil
}
