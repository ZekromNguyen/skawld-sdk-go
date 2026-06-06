package mcp

import "testing"

func TestServerConfigValidationAndNaming(t *testing.T) {
	if err := (ServerConfig{Name: "bad"}).Validate(); err == nil {
		t.Fatal("expected missing transport validation error")
	}
	if err := (ServerConfig{Name: "s", Stdio: &StdioServerConfig{Command: "cmd"}, HTTP: &HTTPServerConfig{URL: "http://example.test"}}).Validate(); err == nil {
		t.Fatal("expected exactly one transport validation error")
	}
	if err := (ServerConfig{Name: "http", HTTP: &HTTPServerConfig{URL: "://bad"}}).Validate(); err == nil {
		t.Fatal("expected invalid url validation error")
	}
	if err := (ServerConfig{Name: "ok", Stdio: &StdioServerConfig{Command: "go"}}).Validate(); err != nil {
		t.Fatalf("expected valid stdio config: %v", err)
	}
	if got := ToolName("My Server", "read-file!"); got != "mcp__My_Server__read_file" {
		t.Fatalf("unexpected tool name: %s", got)
	}
	used := map[string]struct{}{"mcp__s__t": {}}
	if got := UniqueToolName("s", "t", used); got != "mcp__s__t_2" {
		t.Fatalf("unexpected unique tool name: %s", got)
	}
}
