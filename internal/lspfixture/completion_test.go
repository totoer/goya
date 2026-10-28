package lspfixture

import (
	"bytes"
	"testing"
)

func TestCompletionOpaqueNormalizationPreservesBehavior(t *testing.T) {
	a, err := canonicalEditorResult("textDocument/completion", []byte(`{"items":[{"label":"value","data":{"cacheId":1},"textEdit":{"newText":"value","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}}}}],"isIncomplete":false}`))
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"items":[{"label":"value","data":{"handle":"session:generation:token","index":0},"textEdit":{"newText":"value","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}}}}],"isIncomplete":false}`)
	b, err := canonicalEditorResult("textDocument/completion", base)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("opaque encoding not normalized: %s %s %v", a, b, err)
	}
	for _, corrupt := range [][]byte{bytes.Replace(base, []byte(`"newText":"value"`), []byte(`"newText":"wrong"`), 1), bytes.Replace(base, []byte(`"character":2`), []byte(`"character":3`), 1), bytes.Replace(base, []byte(`"label":"value"`), []byte(`"label":"wrong"`), 1)} {
		b, err := canonicalEditorResult("textDocument/completion", corrupt)
		if err != nil || bytes.Equal(a, b) {
			t.Fatalf("normalized meaningful completion behavior: %s %v", b, err)
		}
	}
}
