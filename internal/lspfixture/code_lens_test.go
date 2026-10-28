package lspfixture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCodeLensSourceKeepsIssuedDataAndComparisonKeepsKindAndTargets(t *testing.T) {
	reference := []byte(`[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"data":{"uri":"file:///main.ts","type":0},"command":{"title":"0 references","command":"","arguments":["file:///main.ts",{"line":0,"character":1},[]]}}]`)
	actual := bytes.Replace(reference, []byte(`"type":0`), []byte(`"type":0,"handle":"actual-issued-handle","index":0`), 1)
	selected, err := codeLensAt(actual, 0)
	if err != nil || !bytes.Contains(selected, []byte("actual-issued-handle")) {
		t.Fatalf("issued data: %s %v", selected, err)
	}
	if _, err = codeLensAt(actual, 1); err == nil {
		t.Fatal("out of bounds source")
	}
	a, err := canonicalEditorResult("textDocument/codeLens", reference)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalEditorResult("textDocument/codeLens", actual)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("opaque data: %s %s %v", a, b, err)
	}
	for _, replacement := range []struct{ old, new string }{{`"type":0`, `"type":1`}, {`"character":2`, `"character":3`}, {`"0 references"`, `"1 reference"`}, {`"arguments":[`, `"arguments":[1,`}} {
		wrong := bytes.Replace(actual, []byte(replacement.old), []byte(replacement.new), 1)
		other, err := canonicalEditorResult("textDocument/codeLens", wrong)
		if err != nil || bytes.Equal(a, other) {
			t.Fatalf("normalized meaningful lens data: %s %v", wrong, err)
		}
	}
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/codeLens", Params: json.RawMessage(`{}`), Expected: reference}, {Method: "codeLens/resolve", Params: json.RawMessage(`{}`), Expected: selected, ParamsFromCodeLens: &CodeLensSource{Step: 0, Index: 0}}}
	raw, _ := json.Marshal(f)
	if _, err = Load(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Fixture){func(f *Fixture) { f.Steps[1].ParamsFromCodeLens.Index = 1 }, func(f *Fixture) { f.Steps[1].Method = "textDocument/hover" }, func(f *Fixture) { f.Steps[1].ParamsFromCallHierarchy = &HierarchyItemSource{Step: 0, Index: 0} }} {
		var copy Fixture
		_ = json.Unmarshal(raw, &copy)
		mutate(&copy)
		data, _ := json.Marshal(copy)
		if _, err = Load(strings.NewReader(string(data))); err == nil {
			t.Fatal("accepted invalid lens source")
		}
	}
}
