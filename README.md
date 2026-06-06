# Skawld Agent SDK for Go

This folder is a Go migration of the original TypeScript `@skawld/agent-sdk`.
It keeps the same core ideas:

- `Agent` is the long-lived configuration object.
- `Session` owns conversation state and streams events from `Run`.
- Providers implement a normalized streaming interface.
- Tools implement a common schema/validate/execute contract.
- Permissions, task storage, read-before-edit, and default tools are built in.

The Go module is intentionally Go-native and standard-library first. It includes
direct HTTP/SSE provider adapters for Anthropic Messages, OpenAI Chat
Completions, and OpenAI Responses, MCP tools, skills, subagents, compaction,
and in-memory or SQLite session stores.

```sh
go test ./...
```

Common development checks are available through the `Makefile`:

```sh
make fmt
make vet
make test
```

## Minimal usage

```go
package main

import (
	"context"
	"fmt"
	"os"

	skawld "github.com/skawld/skawld-sdk-go"
	"github.com/skawld/skawld-sdk-go/providers"
	"github.com/skawld/skawld-sdk-go/tools"
)

func main() {
	agent, err := skawld.NewAgent(skawld.AgentOptions{
		Provider: providers.NewOpenAIResponsesProvider(providers.OpenAIOptions{}),
		Model:    "gpt-5",
		Tools:    tools.DefaultTools(),
		Permissions: skawld.PermissionOptions{
			Mode: skawld.PermissionModeDefault,
		},
	})
	if err != nil {
		panic(err)
	}
	defer agent.Close()

	session, err := agent.Session(context.Background(), skawld.SessionOptions{})
	if err != nil {
		panic(err)
	}

	for event := range session.Run(context.Background(), "List files in the current directory.", skawld.RunOptions{}) {
		if event.Type == skawld.EventAssistant {
			for _, block := range event.Message.Content {
				if block.Type == skawld.BlockText {
					fmt.Fprint(os.Stdout, block.Text)
				}
			}
		}
	}
}
```

## Package map

- `github.com/skawld/skawld-sdk-go` exposes `Agent`, `Session`, run options, events, core aliases, and errors.
- `github.com/skawld/skawld-sdk-go/providers` exposes Anthropic/OpenAI provider adapters.
- `github.com/skawld/skawld-sdk-go/tools` exposes `Registry`, `DefaultTools`, and built-in tools.
- `github.com/skawld/skawld-sdk-go/sessions` exposes `InMemoryStore`.
- `github.com/skawld/skawld-sdk-go/sessions/sqlite` exposes persistent SQLite sessions.
- `github.com/skawld/skawld-sdk-go/permissions` exposes `Engine` and rule helpers.
- `github.com/skawld/skawld-sdk-go/config` loads JSON config files.

See [`docs/STRUCTURE.md`](./docs/STRUCTURE.md) for the target module layout and
[`docs/USAGE.md`](./docs/USAGE.md) for provider, tool, permission, and SQLite notes.
Release checks are tracked in [`docs/RELEASE_CHECKLIST.md`](./docs/RELEASE_CHECKLIST.md).
