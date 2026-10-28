package lspfixture

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestHierarchySourceReusesActualDataAndPreservesRanges(t *testing.T) {
	original := json.RawMessage(`[{"uri":"file:///main.ts","selectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"data":{"handle":"actual-session","index":0}}]`)
	selected, err := hierarchyItemAt("textDocument/prepareCallHierarchy", original, 0)
	if err != nil || !bytes.Contains(selected, []byte("actual-session")) {
		t.Fatalf("item source lost actual data: %s %v", selected, err)
	}
	if _, err := hierarchyItemAt("textDocument/prepareCallHierarchy", original, 1); err == nil {
		t.Fatal("accepted out of bounds index")
	}
	if _, err := hierarchyItemAt("textDocument/hover", original, 0); err == nil {
		t.Fatal("accepted unrelated method")
	}
	reference := []byte(`[{"to":{"name":"target","uri":"file:///main.ts","selectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}}},"fromRanges":[{"start":{"line":1,"character":0},"end":{"line":1,"character":4}}]}]`)
	actual := bytes.Replace(reference, []byte(`"name":"target"`), []byte(`"name":"target","data":{"handle":"actual-session","index":0}`), 1)
	a, err := canonicalEditorResult("callHierarchy/outgoingCalls", reference)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalEditorResult("callHierarchy/outgoingCalls", actual)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("opaque metadata not normalized: %s %s %v", a, b, err)
	}
	corrupted := bytes.Replace(actual, []byte(`"character":4`), []byte(`"character":5`), 1)
	c, err := canonicalEditorResult("callHierarchy/outgoingCalls", corrupted)
	if err != nil || bytes.Equal(a, c) {
		t.Fatal("normalized a call range", err)
	}
	nested, err := hierarchyItemAt("callHierarchy/outgoingCalls", actual, 0)
	if err != nil || !bytes.Contains(nested, []byte("actual-session")) {
		t.Fatalf("nested item lost data: %s %v", nested, err)
	}
}
