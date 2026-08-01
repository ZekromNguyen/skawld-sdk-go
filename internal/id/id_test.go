package id

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestNewFromFailsClosedWhenEntropyUnavailable(t *testing.T) {
	value, err := newFrom(failingReader{})
	if err == nil || value != "" {
		t.Fatalf("expected empty id and entropy error, id=%q err=%v", value, err)
	}
}

func TestNewFromProducesUUIDv4(t *testing.T) {
	value, err := newFrom(strings.NewReader(
		"0123456789abcdef",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 36 || value[14] != '4' ||
		!strings.Contains("89ab", string(value[19])) {
		t.Fatalf("unexpected UUIDv4 %q", value)
	}
	if _, err := newFrom(io.LimitReader(
		strings.NewReader("short"), 5,
	)); err == nil {
		t.Fatal("expected short entropy failure")
	}
}
