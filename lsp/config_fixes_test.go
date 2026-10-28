package lsp

import (
	"context"
	"maps"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestConfigurationFixesComposeOnDetachedCatalog(t *testing.T) {
	ctx := context.Background()
	text := "export const value = await Promise.resolve(1);\n"
	config := `{"compilerOptions":{"strict":true,"target":"es2016","module":"es2015","moduleResolution":"bundler","lib":["es2025"],"types":[]},"files":["main.ts"]}`
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: "/tsconfig.json", EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text, "/tsconfig.json": config}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.ts", 3, text); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Snapshot(ctx)
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	result, err := s.CodeActions(ctx, "/main.ts", protocol.Range{End: protocol.Position{Character: uint32(len(text) - 1)}}, protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{}, Only: &kinds}, nil)
	if err != nil || len(result.Actions) != 2 {
		t.Fatalf("configuration proposals: %+v %v", result, err)
	}
	edits := []protocol.TextEdit{}
	for i, proposal := range result.Proposals {
		if proposal == nil {
			t.Fatalf("missing proposal %d", i)
		}
		if err := s.ValidateEdits(ctx, *proposal); err != nil {
			t.Fatal(err)
		}
		for _, change := range proposal.Edit.DocumentChanges {
			document := change.TextDocumentEdit
			if document == nil || document.TextDocument.URI != "file:///tsconfig.json" || document.TextDocument.Version != nil {
				t.Fatalf("config proposal: %+v", change)
			}
			edits = append(edits, document.Edits...)
		}
	}
	copy := before.Project
	copy.Files = maps.Clone(copy.Files)
	copy.Files["/tsconfig.json"] = applyFormattingCopy(t, config, edits)
	if ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("combined configuration fixes: %+v %v", ds, err)
	}
	after, _ := s.Snapshot(ctx)
	if !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("configuration actions changed the live catalog")
	}
}

func TestEnableJSXFixDoesNotInventConfigurationForInferredProject(t *testing.T) {
	ctx := context.Background()
	text := "export const value = <div />;\n"
	declarations := "declare namespace JSX { interface Element {} interface IntrinsicElements { div: {}; } }\ndeclare const React: { createElement: (...args: unknown[]) => JSX.Element };\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.tsx"}, Files: map[string]string{"/main.tsx": text, "/jsx.d.ts": declarations}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.tsx", 2, text); err != nil {
		t.Fatal(err)
	}
	kinds := []protocol.CodeActionKind{protocol.CodeActionQuickFix}
	result, err := s.CodeActions(ctx, "/main.tsx", protocol.Range{End: protocol.Position{Character: uint32(len(text) - 1)}}, protocol.CodeActionContext{Diagnostics: []protocol.Diagnostic{}, Only: &kinds}, nil)
	if err != nil || len(result.Actions) != 0 {
		t.Fatalf("inferred config action: %+v %v", result, err)
	}
	snapshot, _ := s.Snapshot(ctx)
	if snapshot.Project.ConfigFile != "" || len(snapshot.Project.Files) != 2 {
		t.Fatal("configuration fix invented a virtual config")
	}
}
