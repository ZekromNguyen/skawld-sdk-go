package tools

import (
	"fmt"
	"slices"
)

// Profile is an explicit built-in capability set. Production applications
// should choose a profile rather than exposing every built-in tool.
type Profile string

const (
	ProfileMinimal Profile = "minimal"
	ProfileSafe    Profile = "safe"
	ProfileCoding  Profile = "coding"
	ProfileFull    Profile = "full"
)

var profileTools = map[Profile][]string{
	ProfileMinimal: {"Read", "Glob", "Grep"},
	ProfileSafe:    {"Read", "RepoMap", "Glob", "Grep", "TaskList", "TaskGet"},
	ProfileCoding:  {"Read", "RepoMap", "Write", "Edit", "Glob", "Grep", "Bash", "Process", "TaskCreate", "TaskList", "TaskGet", "TaskUpdate", "Subagent"},
	ProfileFull:    nil,
}

// ToolsForProfile returns a new isolated registry for a built-in profile.
func ToolsForProfile(profile Profile) (*Registry, error) {
	all := DefaultTools()
	names, ok := profileTools[profile]
	if !ok {
		return nil, fmt.Errorf("unknown tool profile %q", profile)
	}
	if profile == ProfileFull {
		return all, nil
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	for _, name := range all.Names() {
		if _, ok := allowed[name]; !ok {
			all.Unregister(name)
		}
	}
	return all, nil
}

func ProfileToolNames(profile Profile) []string {
	names, ok := profileTools[profile]
	if !ok {
		return nil
	}
	if profile == ProfileFull {
		return DefaultTools().Names()
	}
	return slices.Clone(names)
}
