package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodeActionClientDiagnosticCodesAndLegacyCommands(t *testing.T) {
	for _, code := range []string{`2420`, `"2420"`, `"client-code"`} {
		raw := []byte(`{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}},"code":` + code + `,"message":"client","data":{"value":1},"codeDescription":{"href":"https://example.invalid/code"}}`)
		var d Diagnostic
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		_ = json.Unmarshal(raw, &before)
		_ = json.Unmarshal(encoded, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("lost diagnostic payload: %s", encoded)
		}
	}
	for _, raw := range []string{`{"title":"legacy","command":"editor.command","arguments":[]}`, `{"title":"Unavailable","kind":"refactor","disabled":{"reason":"selection"},"isPreferred":false}`} {
		var item CommandOrCodeAction
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		_ = json.Unmarshal([]byte(raw), &before)
		_ = json.Unmarshal(encoded, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("union: %s", encoded)
		}
	}
	if _, err := json.Marshal(CommandOrCodeAction{}); err == nil {
		t.Fatal("invalid union accepted")
	}
}
