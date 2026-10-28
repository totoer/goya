package protocol_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestWorkspaceEditResourceOperations(t *testing.T) {
	input := `{"documentChanges":[{"kind":"create","uri":"file:///new.ts"},{"textDocument":{"uri":"file:///new.ts","version":null},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"export {};"}]},{"kind":"rename","oldUri":"file:///old.ts","newUri":"file:///renamed.ts"},{"kind":"delete","uri":"file:///unused.ts"}]}`
	var edit protocol.WorkspaceEdit
	if err := json.Unmarshal([]byte(input), &edit); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(edit)
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("workspace edit changed: %s", encoded)
	}
	if _, err := json.Marshal(protocol.DocumentChange{}); err == nil {
		t.Fatal("empty union accepted")
	}
}
