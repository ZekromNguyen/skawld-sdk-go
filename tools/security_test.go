package tools

import (
	"context"
	"testing"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

func TestNetworkPolicyRejectsPrivateAndUnsafeURLs(t *testing.T) {
	policy := NetworkPolicy{}
	for _, raw := range []string{
		"http://127.0.0.1/admin",
		"http://[::1]/admin",
		"file:///etc/passwd",
		"http://user:password@example.com",
	} {
		if _, err := policy.ValidateURL(context.Background(), raw); err == nil {
			t.Errorf("expected %q to be rejected", raw)
		}
	}
	if _, err := (NetworkPolicy{AllowPrivate: true}).ValidateURL(context.Background(), "http://127.0.0.1/test"); err != nil {
		t.Fatalf("explicit private-network opt-in was rejected: %v", err)
	}
}

func TestSafeProfileExcludesExternalAndMutatingCapabilities(t *testing.T) {
	registry, err := ToolsForProfile(ProfileSafe)
	if err != nil {
		t.Fatal(err)
	}
	for name, descriptor := range registry.Descriptors() {
		if descriptor.NetworkAccess || descriptor.SideEffect != core.SideEffectNone {
			t.Fatalf("safe profile contains unsafe tool %s: %+v", name, descriptor)
		}
	}
	for _, forbidden := range []string{"Bash", "Process", "WebFetch", "SessionSearch", "MemoryRead", "BrowserNavigate"} {
		if _, ok := registry.Get(forbidden); ok {
			t.Errorf("safe profile unexpectedly includes %s", forbidden)
		}
	}
}

func TestFilesystemPolicyDefaultsToCWD(t *testing.T) {
	cwd := t.TempDir()
	if _, err := (FilesystemPolicy{}).Resolve(cwd, "../escape", core.FilesystemResolveRead); err == nil {
		t.Fatal("zero-value filesystem policy allowed cwd escape")
	}
	if _, err := (FilesystemPolicy{Unrestricted: true}).Resolve(cwd, "../escape", core.FilesystemResolveRead); err != nil {
		t.Fatalf("explicit unrestricted compatibility mode failed: %v", err)
	}
}
