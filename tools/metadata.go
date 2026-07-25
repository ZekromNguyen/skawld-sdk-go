package tools

import (
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func readOnlyDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk:        core.RiskLow,
		SideEffect:  core.SideEffectNone,
		Idempotency: core.IdempotencyNotApplicable,
		Timeout:     30 * time.Second,
	}
}

func writeDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk:        core.RiskMedium,
		SideEffect:  core.SideEffectIdempotent,
		Idempotency: core.IdempotencyNotApplicable,
		Timeout:     30 * time.Second,
	}
}

func execDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk:        core.RiskHigh,
		SideEffect:  core.SideEffectUnknown,
		Idempotency: core.IdempotencyUnsupported,
		Timeout:     2 * time.Minute,
		Permissions: []string{"process.execute"},
	}
}

func (ReadTool) ToolDescriptor() core.ToolDescriptor    { return readOnlyDescriptor() }
func (RepoMapTool) ToolDescriptor() core.ToolDescriptor { return readOnlyDescriptor() }
func (GlobTool) ToolDescriptor() core.ToolDescriptor    { return readOnlyDescriptor() }
func (GrepTool) ToolDescriptor() core.ToolDescriptor    { return readOnlyDescriptor() }

func (WriteTool) ToolDescriptor() core.ToolDescriptor { return writeDescriptor() }
func (EditTool) ToolDescriptor() core.ToolDescriptor  { return writeDescriptor() }

func (BashTool) ToolDescriptor() core.ToolDescriptor     { return execDescriptor() }
func (*ProcessTool) ToolDescriptor() core.ToolDescriptor { return execDescriptor() }

func (WebSearchTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := readOnlyDescriptor()
	descriptor.NetworkAccess = true
	descriptor.ContainsUntrusted = true
	descriptor.Permissions = []string{"network.search"}
	return descriptor
}

func (WebFetchTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := readOnlyDescriptor()
	descriptor.Risk = core.RiskMedium
	descriptor.NetworkAccess = true
	descriptor.ContainsUntrusted = true
	descriptor.Permissions = []string{"network.fetch"}
	return descriptor
}

func (*BrowserNavigateTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := writeDescriptor()
	descriptor.NetworkAccess = true
	descriptor.ContainsUntrusted = true
	descriptor.Permissions = []string{"browser.navigate", "network.fetch"}
	return descriptor
}

func (*BrowserSnapshotTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := readOnlyDescriptor()
	descriptor.ContainsUntrusted = true
	descriptor.Permissions = []string{"browser.read"}
	return descriptor
}

func (*BrowserVisionTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := readOnlyDescriptor()
	descriptor.ContainsUntrusted = true
	descriptor.Permissions = []string{"browser.capture"}
	return descriptor
}

func (*CronCreateTool) ToolDescriptor() core.ToolDescriptor {
	return core.ToolDescriptor{
		Risk:        core.RiskHigh,
		SideEffect:  core.SideEffectNonIdempotent,
		Idempotency: core.IdempotencyUnsupported,
		Timeout:     30 * time.Second,
		Permissions: []string{"schedule.create"},
	}
}

func (*CronDeleteTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := writeDescriptor()
	descriptor.Risk = core.RiskHigh
	descriptor.Permissions = []string{"schedule.delete"}
	return descriptor
}

func (*CronListTool) ToolDescriptor() core.ToolDescriptor { return readOnlyDescriptor() }

func (MemoryWriteTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := writeDescriptor()
	descriptor.HandlesSecrets = true
	descriptor.Permissions = []string{"memory.write"}
	return descriptor
}

func (MemoryReadTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := readOnlyDescriptor()
	descriptor.HandlesSecrets = true
	descriptor.Permissions = []string{"memory.read"}
	return descriptor
}

func (MemorySearchTool) ToolDescriptor() core.ToolDescriptor {
	return MemoryReadTool{}.ToolDescriptor()
}

func (SessionSearchTool) ToolDescriptor() core.ToolDescriptor {
	descriptor := MemoryReadTool{}.ToolDescriptor()
	descriptor.Permissions = []string{"session.search"}
	return descriptor
}
