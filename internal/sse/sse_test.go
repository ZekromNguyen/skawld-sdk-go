package sse

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParserMultilineAndCRLF(t *testing.T) {
	parser := NewParser(strings.NewReader("event: message\r\ndata: {\"a\":\r\ndata: 1}\r\n\r\n"), 1024)
	event, ok, err := parser.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected event")
	}
	if event.Name != "message" {
		t.Fatalf("expected event name message, got %q", event.Name)
	}
	if event.Data != "{\"a\":\n1}" {
		t.Fatalf("unexpected data %q", event.Data)
	}
}

func TestParserLargeValidEvent(t *testing.T) {
	data := strings.Repeat("a", 4096)
	parser := NewParser(strings.NewReader("data: "+data+"\n\n"), 4096)
	event, ok, err := parser.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || event.Data != data {
		t.Fatalf("unexpected event ok=%t len=%d", ok, len(event.Data))
	}
}

func TestParserOversizedEvent(t *testing.T) {
	parser := NewParser(strings.NewReader("data: "+strings.Repeat("a", 9)+"\n\n"), 8)
	_, _, err := parser.Next(context.Background())
	var tooLarge EventTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected EventTooLargeError, got %v", err)
	}
}
