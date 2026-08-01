// Package version exposes SDK build identity without coupling callers to a
// particular release tool. Release builds may set Value with -ldflags.
package version

import "runtime/debug"

// Value may be populated with:
// -ldflags "-X github.com/ZekromNguyen/skawld-sdk-go/version.Value=v0.2.0"
var Value = "dev"

func String() string {
	if Value != "" && Value != "dev" {
		return Value
	}
	if info, ok := debug.ReadBuildInfo(); ok &&
		info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
