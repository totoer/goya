package lsp

import (
	"context"
	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
	"testing"
)

func TestExecutionProfileDiagnosticsDirectAndWire(t *testing.T) {
	ctx := context.Background()
	project := goya.Project{Files: map[string]string{"/main.ts": `new Function("return 42");export {};`}}
	s, err := NewSession(ctx, SessionOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check, err := goya.Check(ctx, project, nil, nil)
	if err != nil || len(check) != 1 {
		t.Fatalf("compiler %v %v", check, err)
	}
	direct, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(direct.Items) != 1 {
		t.Fatalf("editor %v %v", direct, err)
	}
	d := direct.Items[0]
	if d.Source != "goya" || d.Code != check[0].Code || d.Range.Start.Character != uint32(check[0].Range.Start.Column-1) {
		t.Fatalf("profile mismatch %+v %+v", d, check[0])
	}
	c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s})
	c.send(t, 2, "textDocument/diagnostic", map[string]any{"textDocument": map[string]any{"uri": "file:///main.ts"}})
	var report protocol.FullDocumentDiagnosticReport
	if err := decodeObject(c.read(t)["result"], &report); err != nil || len(report.Items) != 1 || report.Items[0].Source != d.Source || report.Items[0].Code != d.Code || report.Items[0].Range != d.Range {
		t.Fatalf("wire mismatch %+v %v", report, err)
	}
	c.finish(t)
	if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/main.ts": `function outer(){function Function(s:string){return s.length}return Function("x")}export const result=outer();`}}); err != nil {
		t.Fatal(err)
	}
	plain, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range plain.Items {
		if d.Source == "goya" {
			t.Fatalf("shadowed name rejected %+v", d)
		}
	}
}
