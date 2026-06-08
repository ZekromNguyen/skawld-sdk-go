package frontmatter

import "testing"

func TestParseDocumentSupportsScalarInlineAndBlockLists(t *testing.T) {
	doc := ParseDocument("---\r\nname: review\r\ntools: [Read, 'Grep']\r\nallowed_tools:\r\n  - Bash\r\n  - \"Edit\"\r\n---\r\nBody\r\n")
	if doc.Metadata.String("name") != "review" {
		t.Fatalf("unexpected scalar metadata: %+v", doc.Metadata)
	}
	if got := doc.Metadata.Strings("tools"); len(got) != 2 || got[0] != "Read" || got[1] != "Grep" {
		t.Fatalf("unexpected inline list: %+v", got)
	}
	if got := doc.Metadata.Strings("allowed_tools"); len(got) != 2 || got[0] != "Bash" || got[1] != "Edit" {
		t.Fatalf("unexpected block list: %+v", got)
	}
	if doc.Body != "Body\n" {
		t.Fatalf("unexpected body: %q", doc.Body)
	}
}

func TestParseDocumentWithoutFrontmatterReturnsBody(t *testing.T) {
	doc := ParseDocument("Body only")
	if len(doc.Metadata) != 0 || doc.Body != "Body only" {
		t.Fatalf("unexpected document: %+v", doc)
	}
}
