package structured

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/tools"
)

func TestRegistryCatalogUsesAllowlistAndHidesUntrustedDescriptions(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ReadTool{}); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewRegistryCatalog(CatalogOptions{
		Registry: registry, Names: []string{"Read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := catalog.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].Name != "Read" ||
		definitions[0].Description != "" || definitions[0].DescriptionTrusted {
		t.Fatalf("unexpected model-visible catalog: %+v", definitions)
	}
	if _, exists, err := catalog.Describe(context.Background(), "Bash"); err != nil || exists {
		t.Fatalf("non-allowlisted tool was exposed: exists=%t err=%v", exists, err)
	}
	if digest, err := catalog.ToolCatalogFingerprint(
		context.Background(), []string{"Read"},
	); err != nil || digest == "" {
		t.Fatalf("catalog fingerprint = %q, err=%v", digest, err)
	}
}

func TestRegistryCatalogCanExposeExplicitlyTrustedDescription(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ReadTool{}); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewRegistryCatalog(CatalogOptions{
		Registry: registry, Names: []string{"Read"},
		TrustedDescriptions: map[string]bool{"Read": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := catalog.Definitions()
	if err != nil {
		t.Fatal(err)
	}
	definition := definitions[0]
	if !definition.DescriptionTrusted || definition.Description == "" {
		t.Fatalf("trusted description was not exposed: %+v", definition)
	}
}

func TestRegistryCatalogFailsIfSelectedToolIsRemoved(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ReadTool{}); err != nil {
		t.Fatal(err)
	}
	catalog, err := NewRegistryCatalog(CatalogOptions{
		Registry: registry, Names: []string{"Read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry.Unregister("Read")
	if _, err := catalog.Definitions(); err == nil {
		t.Fatal("expected removed selected tool to invalidate catalog")
	}
}
