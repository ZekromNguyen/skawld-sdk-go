package harness

import "testing"

func TestScenarioRepoMapBeforeEditAndVerify(t *testing.T) {
	Run(t, Scenario{
		Name:   "repo map before edit and verify",
		Prompt: "Update the greeting and verify.",
		Files: map[string]string{
			"go.mod":  "module example.test/app\n\ngo 1.22\n",
			"main.go": "package main\n\nfunc greeting() string { return \"hello\" }\n",
		},
		Provider: []Turn{
			{ToolCalls: []ToolCall{{ID: "map_1", Name: "RepoMap", Input: `{}`}}},
			{ToolCalls: []ToolCall{{ID: "read_1", Name: "Read", Input: `{"file_path":"main.go"}`}}},
			{ToolCalls: []ToolCall{{ID: "edit_1", Name: "Edit", Input: `{"file_path":"main.go","old_string":"return \"hello\"","new_string":"return \"hi\""}`}}},
			{ToolCalls: []ToolCall{{ID: "test_1", Name: "Bash", Input: `{"command":"go test ./...","timeout_ms":120000,"description":"Run Go tests"}`}}},
			{Text: "done"},
		},
		AllowedTools: []string{"RepoMap", "Read", "Edit", "Bash"},
		Checks: []Check{
			ToolOrder("RepoMap", "Edit"),
			ToolOrder("Read", "Edit"),
			ToolOrder("Edit", "Bash"),
			FileContains("main.go", `return "hi"`),
			SuccessfulResult(),
		},
	})
}

func TestScenarioRecoverFromFailedEdit(t *testing.T) {
	Run(t, Scenario{
		Name:   "recover from failed edit",
		Prompt: "Change beta to gamma.",
		Files: map[string]string{
			"go.mod":  "module example.test/app\n\ngo 1.22\n",
			"main.go": "package main\n\nconst value = \"beta\"\n",
		},
		Provider: []Turn{
			{ToolCalls: []ToolCall{{ID: "read_1", Name: "Read", Input: `{"file_path":"main.go"}`}}},
			{ToolCalls: []ToolCall{{ID: "bad_edit", Name: "Edit", Input: `{"file_path":"main.go","old_string":"const value = \"alpha\"","new_string":"const value = \"gamma\""}`}}},
			{ToolCalls: []ToolCall{{ID: "good_edit", Name: "Edit", Input: `{"file_path":"main.go","old_string":"const value = \"beta\"","new_string":"const value = \"gamma\""}`}}},
			{Text: "done"},
		},
		AllowedTools: []string{"Read", "Edit"},
		Checks: []Check{
			ToolOrder("Read", "Edit"),
			FileContains("main.go", `const value = "gamma"`),
			SuccessfulResult(),
		},
	})
}

func TestScenarioReadOnlyOrientation(t *testing.T) {
	Run(t, Scenario{
		Name:   "read only orientation",
		Prompt: "Find the package layout.",
		Files: map[string]string{
			"go.mod":          "module example.test/app\n\ngo 1.22\n",
			"pkg/a/a.go":      "package a\n",
			"pkg/a/a_test.go": "package a\n",
		},
		Provider: []Turn{
			{ToolCalls: []ToolCall{{ID: "map_1", Name: "RepoMap", Input: `{}`}}},
			{Text: "done"},
		},
		AllowedTools: []string{"RepoMap"},
		Checks: []Check{
			ToolCalled("RepoMap"),
			SuccessfulResult(),
		},
	})
}
