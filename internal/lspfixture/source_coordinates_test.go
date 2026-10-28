package lspfixture

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func unicodeCoordinateFixture(t *testing.T) Fixture {
	t.Helper()
	file, err := os.Open("../../testdata/lsp/code-lens-fallback-unicode.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestUnicodeCoordinateProjectionUsesOracleSourceSpansOnly(t *testing.T) {
	f := unicodeCoordinateFixture(t)
	raw := append([]byte(nil), f.Steps[2].Expected...)
	projected, err := projectUnicodeSourceCoordinates(f, 2, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, f.Steps[2].Expected) {
		t.Fatal("changed raw oracle")
	}
	var items []struct {
		Range struct {
			Start, End struct{ Line, Character uint32 }
		}
	}
	if err = json.Unmarshal(projected, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 || items[1].Range.Start.Line != 0 || items[1].Range.Start.Character != 24 || items[1].Range.End.Character != 35 || items[2].Range.Start.Character != 43 || items[3].Range.Start.Line != 1 || items[4].Range.End.Character != 44 {
		t.Fatal(string(projected))
	}
	// Resolution changes only the lens range and command position. It cannot
	// turn a failed reference lookup into a manufactured count or target list.
	changed := bytes.Replace(f.Steps[4].Expected, []byte(`"0 references"`), []byte(`"123 references"`), 1)
	resolved, err := projectUnicodeSourceCoordinates(f, 4, changed)
	if err != nil || !bytes.Contains(resolved, []byte(`"123 references"`)) || !bytes.Contains(resolved, []byte(`"arguments":["{{ROOT_URI}}/main.ts",{"line":0,"character":24},[]]`)) {
		t.Fatalf("semantic fields: %s %v", resolved, err)
	}
	f.Steps[2].UnicodeSourceCoordinates = nil
	unchanged, err := projectUnicodeSourceCoordinates(f, 2, raw)
	if err != nil || !bytes.Equal(unchanged, raw) {
		t.Fatal("implicit projection")
	}
}
func TestUnicodeCoordinateProjectionRejectsUnverifiableEvidence(t *testing.T) {
	original := unicodeCoordinateFixture(t)
	data, _ := json.Marshal(original)
	for _, mutate := range []func(*Fixture){
		func(f *Fixture) { f.Steps[2].UnicodeSourceCoordinates.SourceSHA256 = strings.Repeat("0", 64) },
		func(f *Fixture) { f.Steps[2].UnicodeSourceCoordinates.CodeLenses[1].Span.Start = -1 },
		func(f *Fixture) { f.Steps[2].UnicodeSourceCoordinates.CodeLenses[1].Span.Length = 99999 },
		func(f *Fixture) { f.Steps[2].UnicodeSourceCoordinates.CodeLenses[2].NameSpan.Start = 0 },
		func(f *Fixture) {
			f.Steps[2].UnicodeSourceCoordinates.CodeLenses = f.Steps[2].UnicodeSourceCoordinates.CodeLenses[:1]
		},
		func(f *Fixture) { f.Steps[2].Method = "textDocument/hover" },
		func(f *Fixture) { f.Steps[3].UnicodeSourceCoordinates.SourceSHA256 = "arbitrary" },
		func(f *Fixture) {
			f.Steps[3].Expected = bytes.Replace(f.Steps[3].Expected, []byte(`[]`), []byte(`[{"uri":"file:///unrecorded.ts"}]`), 1)
		},
	} {
		var f Fixture
		_ = json.Unmarshal(data, &f)
		mutate(&f)
		encoded, _ := json.Marshal(f)
		if _, err := Load(bytes.NewReader(encoded)); err == nil {
			t.Fatal("accepted invalid Unicode source evidence")
		}
	}
}
