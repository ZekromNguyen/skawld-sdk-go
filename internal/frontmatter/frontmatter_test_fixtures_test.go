package frontmatter

import "testing"

func TestParseSkillFrontmatter(t *testing.T) {
	// Fixture matching a typical SKILL.md
	doc := ParseDocument("---\nname: code-review\ndescription: Reviews code changes for bugs and style\nwhen_to_use: After writing new code\nargument_hint: Provide the file path\nallowed_tools:\n  - Read\n  - Grep\n  - Glob\nmodel: claude-sonnet-4-6\n---\n\nYou are a code reviewer. Analyze the provided changes.")
	if doc.Metadata.String("name") != "code-review" {
		t.Fatalf("expected name 'code-review', got %q", doc.Metadata.String("name"))
	}
	if doc.Metadata.String("description") != "Reviews code changes for bugs and style" {
		t.Fatalf("unexpected description: %q", doc.Metadata.String("description"))
	}
	if doc.Metadata.String("when_to_use") != "After writing new code" {
		t.Fatalf("unexpected when_to_use: %q", doc.Metadata.String("when_to_use"))
	}
	if doc.Metadata.String("argument_hint") != "Provide the file path" {
		t.Fatalf("unexpected argument_hint: %q", doc.Metadata.String("argument_hint"))
	}
	if doc.Metadata.String("model") != "claude-sonnet-4-6" {
		t.Fatalf("unexpected model: %q", doc.Metadata.String("model"))
	}
	tools := doc.Metadata.Strings("allowed_tools")
	if len(tools) != 3 || tools[0] != "Read" || tools[1] != "Grep" || tools[2] != "Glob" {
		t.Fatalf("unexpected allowed_tools: %v", tools)
	}
	if doc.Body != "\nYou are a code reviewer. Analyze the provided changes." {
		t.Fatalf("unexpected body: %q", doc.Body)
	}
}

func TestParseSubagentFrontmatter(t *testing.T) {
	// Fixture matching a typical subagent .md
	doc := ParseDocument("---\nname: auditor\ndescription: Audits code for security issues\nsystem_prompt: You are a security auditor.\ntools: [Read, Grep, Bash]\nmodel: claude-haiku-4-5\n---\n\nAudit the code and report findings.")
	if doc.Metadata.String("name") != "auditor" {
		t.Fatalf("expected name 'auditor', got %q", doc.Metadata.String("name"))
	}
	if doc.Metadata.String("description") != "Audits code for security issues" {
		t.Fatalf("unexpected description: %q", doc.Metadata.String("description"))
	}
	if doc.Metadata.String("system_prompt") != "You are a security auditor." {
		t.Fatalf("unexpected system_prompt: %q", doc.Metadata.String("system_prompt"))
	}
	if doc.Metadata.String("model") != "claude-haiku-4-5" {
		t.Fatalf("unexpected model: %q", doc.Metadata.String("model"))
	}
	tools := doc.Metadata.Strings("tools")
	if len(tools) != 3 || tools[0] != "Read" || tools[1] != "Grep" || tools[2] != "Bash" {
		t.Fatalf("unexpected tools: %v", tools)
	}
}

func TestParseFrontmatterEmptyTools(t *testing.T) {
	doc := ParseDocument("---\ntools: []\n---\nBody")
	tools := doc.Metadata.Strings("tools")
	if len(tools) != 0 {
		t.Fatalf("expected empty tools, got %v", tools)
	}
}
