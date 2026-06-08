package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestSkawldErrorSupportsErrorsIsAndAsThroughWrap(t *testing.T) {
	providerErr := NewProviderError("temporary", 503, true, errors.New("upstream"))
	wrapped := fmt.Errorf("provider stream failed: %w", providerErr)

	if !errors.Is(wrapped, &SkawldError{Kind: ErrorProvider}) {
		t.Fatalf("expected errors.Is to classify provider error")
	}
	var skerr *SkawldError
	if !errors.As(wrapped, &skerr) || skerr.Status != 503 || !skerr.Retryable {
		t.Fatalf("expected errors.As to preserve provider error, got %#v", skerr)
	}
}
