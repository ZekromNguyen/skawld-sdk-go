package workflow

import "testing"

func TestValidateToolInputUsesTrustedSchema(t *testing.T) {
	schema := map[string]interface{}{
		"type":                 "object",
		"required":             []interface{}{"customer_id"},
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"customer_id": map[string]interface{}{"type": "string"},
		},
	}
	if err := ValidateToolInput(
		schema,
		map[string]interface{}{"customer_id": "customer-1"},
		"customer.lookup",
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateToolInput(
		schema,
		map[string]interface{}{"customer_id": "customer-1", "unsafe": true},
		"customer.lookup",
	); err == nil {
		t.Fatal("unexpected tool input property was accepted")
	}
}

func TestValidateToolSchemasRejectsMalformedContracts(t *testing.T) {
	if err := ValidateToolSchemas(
		map[string]interface{}{"type": "array"},
		map[string]interface{}{"type": "object"},
		"customer.lookup",
	); err == nil {
		t.Fatal("non-object input schema was accepted")
	}
	if err := ValidateToolSchemas(
		map[string]interface{}{"type": "object"},
		map[string]interface{}{
			"properties": map[string]interface{}{"id": "not-a-schema"},
		},
		"customer.lookup",
	); err == nil {
		t.Fatal("malformed output schema was accepted")
	}
}
