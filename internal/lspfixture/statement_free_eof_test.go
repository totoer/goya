package lspfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya/lsp/protocol"
)

func TestStatementFreeEOFPreservesRawOracleAndRejectsBroaderProjection(t *testing.T) {
	file, err := os.Open("../../testdata/lsp/refactor-move-existing-statement-free-whitespace-apply-0.json")
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
	result, err := projectClientRequestEOF(f, n, f.Steps[n].ExpectedClientRequests)
	if err != nil {
		t.Fatal(err)
	}
	var params protocol.ApplyWorkspaceEditParams
	if err := json.Unmarshal(result[0].Params, &params); err != nil {
		t.Fatal(err)
	}
	edit := params.Edit.Changes["{{ROOT_URI}}/target.ts"][0]
	if edit.Range.Start != (protocol.Position{Character: 2}) || edit.Range.End != edit.Range.Start || edit.NewText != "\nexport const value = 1;\n" {
		t.Fatalf("canonical target insertion: %+v", edit)
	}
	after, _ := json.Marshal(f.Steps[n].ExpectedClientRequests)
	if string(before) != string(after) {
		t.Fatal("raw oracle was modified")
	}
	for name, mutate := range map[string]func(*Fixture){
		"hash":        func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].SourceSHA256 = strings.Repeat("0", 64) },
		"offset":      func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].Start-- },
		"replacement": func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].Length = 1 },
		"source-end":  func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].PastEOFInsertion.SourceFileEnd++ },
		"statements":  func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].PastEOFInsertion.StatementCount = 1 },
		"unmarked":    func(f *Fixture) { f.Steps[n].SourceEOF.ClientRequestEdits[0].PastEOFInsertion = nil },
		"tokens": func(f *Fixture) {
			f.Project.Files["/target.ts"] = "{}"
			sum := sha256.Sum256([]byte("{}"))
			f.Steps[n].SourceEOF.ClientRequestEdits[0].SourceSHA256 = hex.EncodeToString(sum[:])
		},
		"action": func(f *Fixture) {
			var p struct {
				Command   string           `json:"command"`
				Arguments []map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(f.Steps[n].Params, &p)
			p.Arguments[0]["action"] = "other"
			f.Steps[n].Params, _ = json.Marshal(p)
		},
		"target": func(f *Fixture) {
			f.Steps[n].Params = json.RawMessage(strings.Replace(string(f.Steps[n].Params), `{{ROOT_PATH}}/target.ts`, `{{ROOT_PATH}}/other.ts`, 1))
		},
		"range": func(f *Fixture) {
			var p protocol.ApplyWorkspaceEditParams
			_ = json.Unmarshal(f.Steps[n].ExpectedClientRequests[0].Params, &p)
			e := p.Edit.Changes["{{ROOT_URI}}/target.ts"]
			e[0].Range.Start.Character = 2
			p.Edit.Changes["{{ROOT_URI}}/target.ts"] = e
			f.Steps[n].ExpectedClientRequests[0].Params, _ = json.Marshal(p)
		},
		"version": func(f *Fixture) { f.Oracle.TypeScriptVersion = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(f)
			var copy Fixture
			_ = json.Unmarshal(raw, &copy)
			mutate(&copy)
			if _, err := projectClientRequestEOF(copy, n, copy.Steps[n].ExpectedClientRequests); err == nil {
				t.Fatal("unverified projection accepted")
			}
		})
	}
	f.Steps[n].SourceEOF = nil
	unchanged, err := projectClientRequestEOF(f, n, f.Steps[n].ExpectedClientRequests)
	if err != nil || !reflect.DeepEqual(unchanged, f.Steps[n].ExpectedClientRequests) {
		t.Fatalf("implicit projection: %v", err)
	}
}

func TestStatementFreeEOFRequiresExplicitSourceShape(t *testing.T) {
	for _, raw := range []string{`{}`, `{"source_file_end":0}`, `{"statement_count":0}`, `{"source_file_end":null,"statement_count":0}`, `{"source_file_end":0,"statement_count":0,"other":true}`} {
		var proof EOFPastEOFInsertion
		if err := json.Unmarshal([]byte(raw), &proof); err == nil {
			t.Fatalf("incomplete source shape accepted: %s", raw)
		}
	}
}
