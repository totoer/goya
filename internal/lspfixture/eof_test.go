package lspfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEOFProjectionKeepsOracleAndUnrelatedRanges(t *testing.T) {
	for _, text := range []string{"x😀\n", "x😀\r\n", "x😀\r", "x😀\u2028", "x😀\u2029", "x😀"} {
		t.Run(text, func(t *testing.T) {
			legacy, end, has := legacyEOF(text)
			if !has {
				legacy = end
			}
			raw, _ := json.Marshal([]any{map[string]any{"range": map[string]any{"start": map[string]int{"line": 0, "character": 1}, "end": legacy}, "parent": map[string]any{"range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 2}}}}})
			f := Fixture{Project: Project{Files: map[string]string{"/main.ts": text}}, Steps: []Step{{Method: "textDocument/selectionRange", Params: json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts"}}`), Expected: raw, SourceEOF: &SourceEOF{}}}}
			original := string(raw)
			projected, err := projectSourceEOF(f, 0, raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(f.Steps[0].Expected) != original {
				t.Fatal("raw oracle was mutated")
			}
			var result []struct {
				Range  struct{ Start, End map[string]uint32 }
				Parent struct {
					Range struct{ End map[string]uint32 }
				}
			}
			if err = json.Unmarshal(projected, &result); err != nil {
				t.Fatal(err)
			}
			if result[0].Range.End["line"] != end.Line || result[0].Range.End["character"] != end.Character || result[0].Range.Start["character"] != 1 || result[0].Parent.Range.End["character"] != 2 {
				t.Fatal(string(projected))
			}
			f.Steps[0].SourceEOF = nil
			unchanged, err := projectSourceEOF(f, 0, raw)
			if err != nil || string(unchanged) != original {
				t.Fatal("implicit projection")
			}
		})
	}
}

func TestEOFProjectionReplaysSequentialUnicodeEditsAndClose(t *testing.T) {
	f := Fixture{Project: Project{Files: map[string]string{"/main.ts": "base\n"}}, Steps: []Step{
		{Method: "textDocument/didOpen", Params: json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts","text":"😀x\r\n"}}`)},
		{Method: "textDocument/didChange", Params: json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts"},"contentChanges":[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"text":"yz"},{"range":{"start":{"line":0,"character":3},"end":{"line":1,"character":0}},"text":"\n"}]}`)},
		{Method: "textDocument/didClose", Params: json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts"}}`)},
	}}
	texts, err := fixtureTexts(f, 1)
	if err != nil || texts["{{ROOT_URI}}/main.ts"] != "😀y\n" {
		t.Fatalf("%v %v", texts, err)
	}
	texts, err = fixtureTexts(f, 2)
	if err != nil || texts["{{ROOT_URI}}/main.ts"] != "base\n" {
		t.Fatalf("%v %v", texts, err)
	}
	f.Steps[1].Params = json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts"},"contentChanges":[{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"text":"x"}]}`)
	if _, err = fixtureTexts(f, 1); err == nil {
		t.Fatal("accepted split surrogate")
	}
}

func TestEOFFoldingProjectionRequiresExactRecordedSpan(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/ranges-linked-incomplete.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	step := f.Steps[1]
	raw := string(step.Expected)
	projected, err := projectSourceEOF(f, 1, step.Expected)
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Steps[1].Expected) != raw || !strings.Contains(string(projected), `"endLine":2`) {
		t.Fatal(string(projected))
	}
	original := *step.SourceEOF
	span := original.FoldingSpans[0]
	for _, mutate := range []func(*EOFFoldingSpan){func(s *EOFFoldingSpan) { s.Length-- }, func(s *EOFFoldingSpan) { s.Start++ }, func(s *EOFFoldingSpan) { s.Index = 1 }, func(s *EOFFoldingSpan) { s.SourceSHA256 = strings.Repeat("0", 64) }} {
		changed := span
		mutate(&changed)
		f.Steps[1].SourceEOF = &SourceEOF{FoldingSpans: []EOFFoldingSpan{changed}}
		if _, err = projectSourceEOF(f, 1, step.Expected); err == nil {
			t.Fatal("accepted wrong EOF outlining evidence")
		}
	}
	f.Steps[1].SourceEOF = &SourceEOF{}
	if _, err = projectSourceEOF(f, 1, step.Expected); err == nil {
		t.Fatal("guessed EOF from folding line")
	}
	f.Steps[1].Method = "textDocument/hover"
	if _, err = projectSourceEOF(f, 1, step.Expected); err == nil {
		t.Fatal("projected unrelated method")
	}
	digest := sha256.Sum256([]byte(f.Project.Files["/main.tsx"]))
	if span.SourceSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("source evidence drift")
	}
}

type eofTape struct{ result json.RawMessage }

func (e eofTape) Notify(context.Context, string, json.RawMessage) error { return nil }
func (e eofTape) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return e.result, nil
}
func TestEOFProjectionDoesNotMaskOtherRangeErrors(t *testing.T) {
	f := Fixture{Project: Project{Files: map[string]string{"/main.ts": "abc\n"}}, Steps: []Step{{Method: "textDocument/selectionRange", Params: json.RawMessage(`{"textDocument":{"uri":"{{ROOT_URI}}/main.ts"}}`), Expected: json.RawMessage(`[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":4}}}]`), SourceEOF: &SourceEOF{}}}}
	correct := json.RawMessage(`[{"range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}}}]`)
	if err := Run(context.Background(), f, "file:///workspace", eofTape{correct}); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{strings.Replace(string(correct), `"character":0`, `"character":1`, 1), string(f.Steps[0].Expected)} {
		if err := Run(context.Background(), f, "file:///workspace", eofTape{json.RawMessage(wrong)}); err == nil {
			t.Fatal("masked a wrong source range")
		}
	}
}
