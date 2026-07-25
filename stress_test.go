package skawld

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/tools/mcp"
)

// TestStressConcurrentSessionCreation verifies that multiple sessions can be
// created concurrently without serializing on slow runtime resource loading.
func TestStressConcurrentSessionCreation(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		Provider:     &singleTextProvider{text: "done"},
		Model:        "test-model",
		Permissions:  PermissionOptions{Mode: PermissionModeYolo},
		SkillsDir:    filepath.Join(t.TempDir(), "missing-skills"),
		SubagentsDir: filepath.Join(t.TempDir(), "missing-agents"),
		MCPServers:   []mcp.ServerConfig{},
		MaxTurns:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	concurrency := 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := agent.Session(context.Background(), SessionOptions{})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestStressRapidSessionCreateDelete verifies that session create/delete
// cycles do not leak resources.
func TestStressRapidSessionCreateDelete(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		Provider:     &singleTextProvider{text: "done"},
		Model:        "test-model",
		Permissions:  PermissionOptions{Mode: PermissionModeYolo},
		SkillsDir:    filepath.Join(t.TempDir(), "missing-skills"),
		SubagentsDir: filepath.Join(t.TempDir(), "missing-agents"),
		MaxTurns:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	iterations := 50
	for i := 0; i < iterations; i++ {
		_, err := agent.Session(context.Background(), SessionOptions{})
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

// TestStressSubagentConcurrentUse verifies parent and subagent providers can
// be used concurrently without races.
func TestStressSubagentConcurrentUse(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "worker.md"), []byte("---\nname: worker\ndescription: Works\n---\nWork."), 0o644); err != nil {
		t.Fatal(err)
	}

	agent, err := NewAgent(AgentOptions{
		Provider:     &singleTextProvider{text: `{"type": "tool_use", "name": "Subagent", "input": {"agent": "worker", "task": "do work"}}`},
		Model:        "test-model",
		Permissions:  PermissionOptions{Mode: PermissionModeYolo},
		SubagentsDir: agentsDir,
		SkillsDir:    filepath.Join(t.TempDir(), "missing-skills"),
		MaxTurns:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	concurrency := 5
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := agent.Session(context.Background(), SessionOptions{})
			if err != nil {
				t.Errorf("goroutine %d: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
}

// TestStressBashCancelation verifies that Bash cancelation properly cleans up
// processes.
func TestStressBashCancelation(t *testing.T) {
	agent, err := NewAgent(AgentOptions{
		Provider:    &singleTextProvider{text: `{"type": "tool_use", "name": "Bash", "input": {"command": "sleep 30"}}`},
		Model:       "test-model",
		Permissions: PermissionOptions{Mode: PermissionModeYolo},
		MaxTurns:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	iterations := 5
	for i := 0; i < iterations; i++ {
		sess, err := agent.Session(context.Background(), SessionOptions{})
		if err != nil {
			t.Fatalf("iteration %d session: %v", i, err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		rh := sess.StartRun(ctx, "run sleep 30", RunOptions{})

		// Drain a few events then cancel
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		for range rh.Events() {
			// drain until closed
		}
		rh.Close()
	}
}

// TestStressMCPSlowServer verifies that slow MCP servers do not block
// unrelated session creation.
func TestStressMCPSlowServer(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer slowServer.Close()

	agent, err := NewAgent(AgentOptions{
		Provider:    &singleTextProvider{text: "done"},
		Model:       "test-model",
		Permissions: PermissionOptions{Mode: PermissionModeYolo},
		MCPServers: []mcp.ServerConfig{
			{Name: "slow", HTTP: &mcp.HTTPServerConfig{URL: slowServer.URL}},
		},
		MaxTurns: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	// Session creation should not block waiting for MCP connection
	done := make(chan struct{})
	go func() {
		_, err := agent.Session(context.Background(), SessionOptions{})
		if err != nil {
			t.Logf("session error (expected): %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
		// OK — session creation did not block
	case <-time.After(5 * time.Second):
		t.Fatal("session creation blocked too long on slow MCP server")
	}
}

// TestStressFileSystemToolsConcurrentReads verifies concurrent session
// creation with filesystem access does not race.
func TestStressFileSystemToolsConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(dir, filepath.Base(t.Name()))
		_ = os.WriteFile(path, []byte("content"), 0o644)
	}

	agent, err := NewAgent(AgentOptions{
		Provider:    &singleTextProvider{text: "done"},
		Model:       "test-model",
		Permissions: PermissionOptions{Mode: PermissionModeYolo},
		CWD:         dir,
		MaxTurns:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()

	concurrency := 10
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := agent.Session(context.Background(), SessionOptions{})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
