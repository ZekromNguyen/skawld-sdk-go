Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
  gofmt -w .
  go vet ./...
  go test ./...
  go test ./examples/...
  go test -run "TestParallelSafeToolsRunConcurrentlyAndPreserveResultOrder|TestNonParallelToolsRemainSerialized|TestReadSupportsOffsetsAndTruncatesLongLines|TestEditPreservesCRLFLineEndings" ./...
  Write-Host "Sprint 2 verification passed."
} finally {
  Pop-Location
}
