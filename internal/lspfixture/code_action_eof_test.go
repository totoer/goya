package lspfixture

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEOFCodeActionProjectionUsesPinnedSpansAndPreservesTextAndOtherFields(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/fix-empty-export.json")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	index := len(f.Steps) - 1
	original := string(f.Steps[index].Expected)
	projected, err := projectSourceEOF(f, index, f.Steps[index].Expected)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Steps[index].Expected) != original || !bytes.Contains(projected, []byte(`"line":1`)) || !bytes.Contains(projected, []byte(`"offset":1`)) {
		t.Fatalf("projection: %s %v", projected, err)
	}
	// Only coordinates can change. Text, preferred status, count, command ID and
	// diagnostic content remain fully compared even under the explicit policy.
	for _, change := range []struct{ old, new string }{
		{`export { };`, `export { value };`}, {`"isPreferred": false`, `"isPreferred": true`},
		{`_typescript.applyCodeActionCommand`, `_typescript.otherCommand`},
	} {
		modified := bytes.Replace(f.Steps[index].Expected, []byte(change.old), []byte(change.new), -1)
		if bytes.Equal(modified, f.Steps[index].Expected) {
			t.Fatalf("test did not change %s", change.old)
		}
		other, err := projectSourceEOF(f, index, modified)
		if err == nil {
			a, _ := canonical(projected)
			b, _ := canonical(other)
			if bytes.Equal(a, b) {
				t.Fatal("projection erased a meaningful difference")
			}
		}
	}
	data, _ := json.Marshal(f)
	for _, mutate := range []func(*Fixture){
		func(f *Fixture) { f.Oracle.TypeScriptVersion = "other" },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].Start-- },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].Length = 1 },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].SourceSHA256 = strings.Repeat("0", 64) },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].Action = 1 },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].DocumentChange = 1 },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].Edit = 1 },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].URI = "{{ROOT_URI}}/other.ts" },
		func(f *Fixture) { f.Steps[index].SourceEOF.CodeActionEdits[0].CommandFile = nil },
		func(f *Fixture) {
			f.Steps[index].SourceEOF.CodeActionEdits = append(f.Steps[index].SourceEOF.CodeActionEdits, f.Steps[index].SourceEOF.CodeActionEdits[0])
		},
		func(f *Fixture) { f.Steps[index].SourceEOF = &SourceEOF{} },
		func(f *Fixture) { f.Steps[index].Method = "textDocument/hover" },
	} {
		var copy Fixture
		_ = json.Unmarshal(data, &copy)
		mutate(&copy)
		if _, err := projectSourceEOF(copy, index, copy.Steps[index].Expected); err == nil {
			t.Fatal("accepted invalid EOF action evidence")
		}
	}
	// A wrong non-EOF position is an error, rather than an override opportunity.
	malformed := bytes.Replace(f.Steps[index].Expected, []byte(`"character": 26`), []byte(`"character": 24`), -1)
	if _, err := projectSourceEOF(f, index, malformed); err == nil {
		t.Fatal("overrode a wrong action range")
	}
}

func TestEOFCodeActionProjectionPreservesNonEmptySourceSpan(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/imports-sort-exports.json")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := Load(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	index := len(fixture.Steps) - 1
	raw := fixture.Steps[index].Expected
	projected, err := projectSourceEOF(fixture, index, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Recorded raw span 28+36 reaches EOF; start remains line 1, column 0.
	var actions []struct {
		Edit struct {
			DocumentChanges []struct {
				Edits []struct {
					Range   struct{ Start, End struct{ Line, Character int } }
					NewText string
				}
			}
		}
	}
	if err := json.Unmarshal(projected, &actions); err != nil {
		t.Fatal(err)
	}
	edits := actions[0].Edit.DocumentChanges[0].Edits
	if edits[1].Range.Start.Line != 1 || edits[1].Range.Start.Character != 0 || edits[1].Range.End.Line != 2 || edits[1].Range.End.Character != 0 || edits[1].NewText != "" {
		t.Fatalf("projected EOF deletion: %s", projected)
	}
	if bytes.Equal(raw, projected) {
		t.Fatal("projection did not correct the legacy end")
	}
	fixture.Steps[index].SourceEOF.CodeActionEdits[0].Start--
	fixture.Steps[index].SourceEOF.CodeActionEdits[0].Length++
	if _, err := projectSourceEOF(fixture, index, raw); err == nil {
		t.Fatal("accepted a source span with the wrong non-EOF start")
	}
}

func TestEOFFixAllProjectionKeepsIdentityAndRejectsUnmappedCommandPayloads(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/fix-type-only-export-all.json")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Load(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	index := len(f.Steps) - 1
	raw := f.Steps[index].Expected
	projected, err := projectSourceEOF(f, index, raw)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(raw, &before) != nil || json.Unmarshal(projected, &after) != nil {
		t.Fatal("malformed test action")
	}
	a, _ := canonical(before["command"])
	b, _ := canonical(after["command"])
	if !bytes.Equal(a, b) {
		t.Fatal("changed fix-all continuation identity")
	}
	for _, replacement := range []string{
		`{"command":"_typescript.other","arguments":[{"tsActionId":"convertToTypeOnlyExport"}]}`,
		`{"command":"_typescript.applyFixAllCodeAction","arguments":[{"tsActionId":""}]}`,
		`{"command":"_typescript.applyFixAllCodeAction","arguments":[{"tsActionId":"convertToTypeOnlyExport","changes":[]}]}`,
	} {
		before["command"] = json.RawMessage(replacement)
		changed, _ := json.Marshal(before)
		if _, err := projectSourceEOF(f, index, changed); err == nil {
			t.Fatal("accepted a command without recorded source mappings")
		}
	}
}
