package lspfixture

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestRecordedClientEOFProjectsOnlyVerifiedRange(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/refactor-export-const-typed-apply-0.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	f, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	n := len(f.Steps) - 1
	before, _ := json.Marshal(f.Steps[n].ExpectedClientRequests)
	projected, err := projectClientRequestEOF(f, n, f.Steps[n].ExpectedClientRequests)
	if err != nil {
		t.Fatal(err)
	}
	var params protocol.ApplyWorkspaceEditParams
	if err := json.Unmarshal(projected[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	edits := params.Edit.Changes["{{ROOT_URI}}/main.ts"]
	if len(edits) != 2 || edits[0].Range.End.Character != 7 || edits[1].Range.Start != (protocol.Position{Line: 1}) || edits[1].Range.End != edits[1].Range.Start || edits[1].NewText != "export default value;\n" {
		t.Fatalf("projected proposal: %+v", params)
	}
	after, _ := json.Marshal(f.Steps[n].ExpectedClientRequests)
	if string(before) != string(after) {
		t.Fatal("raw oracle payload changed")
	}
	for name, mutate := range map[string]func(*Fixture){
		"hash":  func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].SourceSHA256 = strings.Repeat("0", 64) },
		"start": func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].Start-- },
		"non-eof": func(f *Fixture) {
			f.Steps[n].SourceEOF.ClientRequestEdits[0].Start--
			f.Steps[n].SourceEOF.ClientRequestEdits[0].Length++
		},
		"uri":     func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].URI = "{{ROOT_URI}}/other.ts" },
		"request": func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].Request = 1 },
		"edit":    func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].Edit = 0 },
		"duplicate": func(f *Fixture) {
			p := f.Steps[n].SourceEOF
			p.ClientRequestEdits = append(p.ClientRequestEdits, p.ClientRequestEdits[0])
		},
		"method":  func(f *Fixture) { f.Steps[n].ExpectedClientRequests[0].Method = "_typescript.rename" },
		"version": func(f *Fixture) { f.Oracle.TypeScriptVersion = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(f)
			var copy Fixture
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			mutate(&copy)
			if _, err := projectClientRequestEOF(copy, n, copy.Steps[n].ExpectedClientRequests); err == nil {
				t.Fatal("unverified range accepted")
			}
		})
	}
	// Without an explicit policy even a legacy presentation remains untouched.
	f.Steps[n].SourceEOF = nil
	unchanged, err := projectClientRequestEOF(f, n, f.Steps[n].ExpectedClientRequests)
	if err != nil || !reflect.DeepEqual(unchanged, f.Steps[n].ExpectedClientRequests) {
		t.Fatalf("implicit normalization: %v", err)
	}
}
