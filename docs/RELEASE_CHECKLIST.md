# Release Checklist

- Run `gofmt -w .`.
- Run `go vet ./...`.
- Run `go test ./...`.
- Run `go test ./examples/...`.
- Verify provider smoke tests against live OpenAI and Anthropic credentials.
- Verify at least one stdio MCP server and one HTTP MCP server.
- Verify `.skawld/skills` and `.skawld/agents` loading in a sample repository.
- Verify production workflow SQLite opens with `RequireProtection` and the
  deployment tenant-key provider.
- Run one leased audit worker through delivery failure, backoff, dead-letter,
  inspection, and requeue.
- Verify approval grant/reject/cancel capabilities and separation-of-duties
  rules against production identity claims.
- Verify every side-effecting production tool has either a stable idempotency
  strategy or a deterministic reconciliation adapter.
- Run two workflow workers against the same checkpoint and verify claim
  exclusion, heartbeat renewal, fencing after expiry, and clean release.
- Verify workflow timeout and explicit cancellation close pending approvals and
  produce durable cancellation audit events.
- Rotate a staging tenant key, re-protect every document, verify reads, and only
  then retire the historical key.
- Verify adapter sensitivity and recorder redaction rules against representative
  confidential and restricted payloads.
- Review and execute the configured tenant retention policy in a staging copy.
- Review known gaps in `TODO.md` before tagging.

Known gaps intentionally left open:

- Memory session parity test item in `SCRUM_PLAN.md`.
- Broad permissions/tool parity rollups in `TODO.md` remain open until every fixture from the TypeScript SDK is tracked one-for-one.
- Live OpenAI, Anthropic, stdio MCP, and HTTP MCP smoke tests require release
  credentials and environments.
