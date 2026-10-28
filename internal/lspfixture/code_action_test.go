package lspfixture

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCodeActionSourceKeepsIssuedHandleAndComparisonKeepsEdits(t *testing.T) {
	reference := []byte(`[{"title":"Fix all","kind":"quickfix","isPreferred":false,"data":{"providerId":2,"globalId":1},"edit":{"changes":{"file:///main.ts":[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"newText":"value"}]}}}]`)
	actual := bytes.Replace(reference, []byte(`{"providerId":2,"globalId":1}`), []byte(`{"handle":"issued","index":3}`), 1)
	a, err := canonicalCodeAction("textDocument/codeAction", reference)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalCodeAction("textDocument/codeAction", actual)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("opaque data: %s %s %v", a, b, err)
	}
	issued, err := codeActionAt(actual, 0)
	if err != nil || !bytes.Contains(issued, []byte("issued")) {
		t.Fatalf("source: %s %v", issued, err)
	}
	if _, err = codeActionAt(actual, 1); err == nil {
		t.Fatal("out-of-bounds action")
	}
	for _, change := range []struct{ old, new string }{{`"newText":"value"`, `"newText":"wrong"`}, {`"isPreferred":false`, `"isPreferred":true`}, {`"character":2`, `"character":3`}, {`"title":"Fix all"`, `"title":"Other"`}} {
		wrong := bytes.Replace(actual, []byte(change.old), []byte(change.new), 1)
		c, err := canonicalCodeAction("textDocument/codeAction", wrong)
		if err != nil || bytes.Equal(a, c) {
			t.Fatalf("normalized semantic action fields: %s %v", wrong, err)
		}
	}
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/codeAction", Params: json.RawMessage(`{}`), Expected: reference}, {Method: "codeAction/resolve", Params: json.RawMessage(`{}`), Expected: issued, ParamsFromCodeAction: &CodeActionSource{Step: 0, Index: 0}}}
	raw, _ := json.Marshal(f)
	if _, err = Load(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	f.Steps[1].ParamsFromCodeAction.Index = 1
	raw, _ = json.Marshal(f)
	if _, err = Load(bytes.NewReader(raw)); err == nil {
		t.Fatal("accepted invalid action source")
	}
	expanded, err := expand([]byte(`{"uri":"{{ROOT_URI}}/main.ts","file":"{{ROOT_PATH}}/main.ts"}`), "file:///fixture%20workspace")
	if err != nil || !bytes.Contains(expanded, []byte(`"file":"/fixture workspace/main.ts"`)) {
		t.Fatalf("root path: %s %v", expanded, err)
	}
}
