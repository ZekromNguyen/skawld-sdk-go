# Release Checklist

- Run `gofmt -w .`.
- Run `go vet ./...`.
- Run `go test ./...`.
- Run `go test ./examples/...`.
- Verify provider smoke tests against live OpenAI and Anthropic credentials.
- Verify at least one stdio MCP server and one HTTP MCP server.
- Verify `.skawld/skills` and `.skawld/agents` loading in a sample repository.
- Review known gaps in `TODO.md` before tagging.

Known gaps intentionally left open:

- Abandoned run iterator cleanup semantics.
- Memory session parity test item in `SCRUM_PLAN.md`.
- Broad permissions/tool parity rollups in `TODO.md` remain open until every fixture from the TypeScript SDK is tracked one-for-one.
