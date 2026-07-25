package structured

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

// CatalogOptions derives the model-visible learning catalog from registered
// tools. Names is a required allowlist; descriptions remain hidden unless
// individually marked trusted.
type CatalogOptions struct {
	Registry            *tools.Registry
	Names               []string
	TrustedDescriptions map[string]bool
}

// RegistryCatalog is one source of truth for extractor definitions, compiler
// validation, and contract drift checks.
type RegistryCatalog struct {
	registry            *tools.Registry
	names               map[string]struct{}
	orderedNames        []string
	trustedDescriptions map[string]bool
}

func NewRegistryCatalog(options CatalogOptions) (*RegistryCatalog, error) {
	if options.Registry == nil {
		return nil, core.NewConfigError("structured tool catalog requires a registry")
	}
	names := make([]string, len(options.Names))
	for index, name := range options.Names {
		names[index] = strings.TrimSpace(name)
	}
	sort.Strings(names)
	// Validate the explicit selection and every registered contract up front.
	if _, err := tools.CatalogFingerprint(options.Registry, names); err != nil {
		return nil, core.NewConfigError("invalid structured tool catalog: " + err.Error())
	}
	catalog := &RegistryCatalog{
		registry: options.Registry, names: make(map[string]struct{}, len(names)),
		orderedNames:        append([]string(nil), names...),
		trustedDescriptions: make(map[string]bool, len(options.TrustedDescriptions)),
	}
	for _, name := range names {
		catalog.names[name] = struct{}{}
		catalog.trustedDescriptions[name] = options.TrustedDescriptions[name]
	}
	return catalog, nil
}

// Definitions derives a fresh snapshot suitable for structured extractor
// configuration. A tool removed after catalog construction fails closed.
func (c *RegistryCatalog) Definitions() ([]ToolDefinition, error) {
	if c == nil || c.registry == nil {
		return nil, core.NewConfigError("structured tool catalog is nil")
	}
	output := make([]ToolDefinition, 0, len(c.orderedNames))
	for _, name := range c.orderedNames {
		tool, exists := c.registry.Get(name)
		if !exists {
			return nil, fmt.Errorf("selected tool %q is no longer registered", name)
		}
		descriptor := core.DescribeTool(tool)
		trusted := c.trustedDescriptions[name]
		definition := ToolDefinition{
			Name: name, DescriptionTrusted: trusted,
			InputSchema: tool.InputSchema(), OutputSchema: descriptor.OutputSchema,
		}
		if trusted {
			definition.Description = tool.Description()
		}
		output = append(output, definition)
	}
	return output, nil
}

func (c *RegistryCatalog) Describe(
	ctx context.Context,
	name string,
) (core.ToolDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolDescriptor{}, false, err
	}
	if c == nil || c.registry == nil {
		return core.ToolDescriptor{}, false, core.NewConfigError("structured tool catalog is nil")
	}
	if _, selected := c.names[name]; !selected {
		return core.ToolDescriptor{}, false, nil
	}
	tool, exists := c.registry.Get(name)
	if !exists {
		return core.ToolDescriptor{}, false, fmt.Errorf("selected tool %q is no longer registered", name)
	}
	return core.DescribeTool(tool), true, nil
}

func (c *RegistryCatalog) ToolCatalogFingerprint(
	ctx context.Context,
	names []string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c == nil || c.registry == nil {
		return "", core.NewConfigError("structured tool catalog is nil")
	}
	for _, name := range names {
		if _, selected := c.names[name]; !selected {
			return "", fmt.Errorf("tool %q is outside the structured catalog allowlist", name)
		}
	}
	return tools.CatalogFingerprint(c.registry, names)
}
