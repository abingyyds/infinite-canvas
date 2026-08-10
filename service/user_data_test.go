package service

import (
	"encoding/json"
	"testing"
)

func storedProjects(pairs ...string) map[string]json.RawMessage {
	stored := map[string]json.RawMessage{}
	for i := 0; i < len(pairs); i += 2 {
		stored[pairs[i]] = json.RawMessage(pairs[i+1])
	}
	return stored
}

func joined(projects []json.RawMessage) string {
	out, err := json.Marshal(projects)
	if err != nil {
		panic(err)
	}
	return string(out)
}

func TestMergeCanvasProjectsReplacesOnlyPatchedProject(t *testing.T) {
	stored := storedProjects(
		"a", `{"id":"a","title":"old A"}`,
		"b", `{"id":"b","title":"B"}`,
	)
	got, err := mergeCanvasProjects(stored, CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"id":"a","title":"new A"}`)},
		KeepIDs:  []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	want := `[{"id":"a","title":"new A"},{"id":"b","title":"B"}]`
	if joined(got) != want {
		t.Errorf("merged = %s, want %s", joined(got), want)
	}
}

func TestMergeCanvasProjectsDropsIDsMissingFromKeepIDs(t *testing.T) {
	stored := storedProjects(
		"a", `{"id":"a","title":"A"}`,
		"b", `{"id":"b","title":"B"}`,
		"c", `{"id":"c","title":"C"}`,
	)
	got, err := mergeCanvasProjects(stored, CanvasProjectsPatch{KeepIDs: []string{"c", "a"}})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	want := `[{"id":"c","title":"C"},{"id":"a","title":"A"}]`
	if joined(got) != want {
		t.Errorf("merged = %s, want %s", joined(got), want)
	}
}

func TestMergeCanvasProjectsInsertsNewProject(t *testing.T) {
	got, err := mergeCanvasProjects(storedProjects(), CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"id":"new","title":"N"}`)},
		KeepIDs:  []string{"new"},
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if joined(got) != `[{"id":"new","title":"N"}]` {
		t.Errorf("merged = %s", joined(got))
	}
}

func TestMergeCanvasProjectsRejectsProjectWithoutID(t *testing.T) {
	_, err := mergeCanvasProjects(storedProjects(), CanvasProjectsPatch{
		Projects: []json.RawMessage{json.RawMessage(`{"title":"no id"}`)},
		KeepIDs:  []string{"x"},
	})
	if err == nil {
		t.Fatal("expected an error for a project without id")
	}
	if err.Error() != "画布数据缺少 id" {
		t.Errorf("error = %q, want %q", err.Error(), "画布数据缺少 id")
	}
}

// KeepIDs 引用了服务端没有、这次也没上传的 id 时跳过，而不是塞进 null。
func TestMergeCanvasProjectsSkipsUnknownKeepID(t *testing.T) {
	got, err := mergeCanvasProjects(storedProjects("a", `{"id":"a"}`), CanvasProjectsPatch{KeepIDs: []string{"a", "ghost"}})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if joined(got) != `[{"id":"a"}]` {
		t.Errorf("merged = %s, want [{\"id\":\"a\"}]", joined(got))
	}
}
