package tools

import (
	"reflect"
	"testing"

	"github.com/skawld/skawld-sdk-go/core"
)

func TestTypedFilesystemInputParsersNormalizeValues(t *testing.T) {
	read, err := parseReadInput(map[string]interface{}{"file_path": "a.txt", "offset": float64(0), "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if read.FilePath != "a.txt" || read.Offset != 1 || read.Limit != 2 {
		t.Fatalf("unexpected read input: %+v", read)
	}
	if got := read.mapValue(); got["offset"] != 1 || got["limit"] != 2 {
		t.Fatalf("unexpected read map: %+v", got)
	}

	grep, err := parseGrepInput(map[string]interface{}{"pattern": "TODO", "glob": `**\*.go`, "-C": "3", "head_limit": float64(10)})
	if err != nil {
		t.Fatal(err)
	}
	if grep.Glob != "**/*.go" || grep.Context == nil || *grep.Context != 3 || grep.HeadLimit != 10 {
		t.Fatalf("unexpected grep input: %+v", grep)
	}
}

func TestTypedInputParsersRejectInvalidValues(t *testing.T) {
	if _, err := parseWriteInput(map[string]interface{}{"file_path": "out.txt", "content": 3}); err == nil {
		t.Fatal("expected non-string content to fail")
	}
	if _, err := parseGrepInput(map[string]interface{}{"pattern": "x", "output_mode": "bad"}); err == nil {
		t.Fatal("expected invalid grep mode to fail")
	}
	if _, err := parseBashInput(map[string]interface{}{"command": "  "}); err == nil {
		t.Fatal("expected blank command to fail")
	}
	if _, err := parseTaskUpdateInput(map[string]interface{}{"id": "1", "add_blocks": []interface{}{"2", 3}}); err == nil {
		t.Fatal("expected invalid task ids to fail")
	}
}

func TestTypedBashInputClampsTimeout(t *testing.T) {
	minInput, err := parseBashInput(map[string]interface{}{"command": "echo ok", "timeout_ms": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if minInput.TimeoutMS != 100 {
		t.Fatalf("expected minimum timeout, got %+v", minInput)
	}
	maxInput, err := parseBashInput(map[string]interface{}{"command": "echo ok", "timeout_ms": 9999999})
	if err != nil {
		t.Fatal(err)
	}
	if maxInput.TimeoutMS != 1800000 {
		t.Fatalf("expected maximum timeout, got %+v", maxInput)
	}
}

func TestTypedTaskUpdateInputBuildsPatch(t *testing.T) {
	in, err := parseTaskUpdateInput(map[string]interface{}{
		"id":             "1",
		"status":         "completed",
		"subject":        "done",
		"add_blocked_by": []interface{}{"2", "3"},
		"metadata":       map[string]interface{}{"drop": nil},
		"delete":         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	patch := in.patch()
	if patch.Subject == nil || *patch.Subject != "done" {
		t.Fatalf("unexpected subject patch: %+v", patch)
	}
	if patch.Status == nil || *patch.Status != core.TaskCompleted {
		t.Fatalf("unexpected status patch: %+v", patch)
	}
	if !reflect.DeepEqual(patch.AddBlockedBy, []string{"2", "3"}) || !patch.Delete {
		t.Fatalf("unexpected edge/delete patch: %+v", patch)
	}
	if _, ok := in.mapValue()["metadata"].(map[string]interface{}); !ok {
		t.Fatalf("expected metadata in normalized map: %+v", in.mapValue())
	}
}
