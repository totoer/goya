package lsp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestMoveRefactorsOwnResourcesAndWaitForHostChanges(t *testing.T) {
	for _, toNew := range []bool{true, false} {
		name := "existing"
		if toNew {
			name = "new"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			text := "const value = 1;\nexport const result = value + 1;\n"
			project := goya.Project{Files: map[string]string{"/main.ts": text, "/target.ts": "export const other = 2;\n"}, EntryPoints: []string{"/main.ts", "/target.ts"}}
			s, err := NewSession(ctx, SessionOptions{Project: project, CompilerOptions: goya.CompileOptions{"strict": true, "target": "es2025", "module": "esnext", "moduleResolution": "bundler"}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "catalog:///user/main.ts", "/value.ts": "catalog:///future/value.ts", "/target.ts": "catalog:///user/target.ts"}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			kind := protocol.CodeActionKind("refactor.move.file")
			if toNew {
				kind = "refactor.move.newFile"
			}
			end, _ := PositionAt(text, strings.Index(text, ";\n")+1)
			choices, err := s.ApplicableRefactors(ctx, "/main.ts", protocol.Range{End: end}, &RefactorRequestOptions{Kind: &kind})
			if err != nil || len(choices.Refactors) != 1 || len(choices.Refactors[0].Actions) != 1 || choices.Refactors[0].Actions[0].NotApplicableReason != "" {
				t.Fatalf("move choices: %+v %v", choices, err)
			}
			before, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var args *RefactorArguments
			if !toNew {
				args = &RefactorArguments{TargetFile: "/target.ts"}
			}
			result, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle}, args)
			if err != nil || result.Edits == nil || result.Rename != nil {
				t.Fatalf("move proposal: %+v %v", result, err)
			}
			created := []protocol.DocumentURI{}
			for _, change := range result.Edits.Edit.DocumentChanges {
				if change.CreateFile != nil {
					created = append(created, change.CreateFile.URI)
				}
			}
			if toNew {
				if !slices.Equal(created, []protocol.DocumentURI{"catalog:///future/value.ts"}) {
					t.Fatalf("create resources: %+v", created)
				}
			} else if len(created) != 0 {
				t.Fatalf("existing target was recreated: %+v", created)
			}
			called := false
			hook := func(_ context.Context, _ protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
				called = true
				return protocol.ApplyWorkspaceEditResult{Applied: false}, nil
			}
			if toNew {
				_, err = s.ApplyEdits(ctx, *result.Edits, "", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true, DocumentChanges: true}, ApplyEdit: hook})
				if !isUnsupportedMoveClient(err) || called {
					t.Fatalf("unsupported resource client: %v callback=%v", err, called)
				}
			}
			response, err := s.ApplyEdits(ctx, *result.Edits, "", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true, DocumentChanges: true, ResourceOperations: []protocol.ResourceOperationKind{protocol.ResourceCreate}}, ApplyEdit: hook})
			if err != nil || response.Applied || !called {
				t.Fatalf("declined move: %+v %v", response, err)
			}
			response, err = s.ApplyEdits(ctx, *result.Edits, "", ClientHooks{Capabilities: ClientCapabilities{ApplyEdit: true, DocumentChanges: true, ResourceOperations: []protocol.ResourceOperationKind{protocol.ResourceCreate}}, ApplyEdit: func(_ context.Context, _ protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
				return protocol.ApplyWorkspaceEditResult{Applied: true}, nil
			}})
			if err != nil || !response.Applied {
				t.Fatalf("accepted move: %+v %v", response, err)
			}
			after, err := s.Snapshot(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("client response changed catalog: %v", err)
			}
			project.Files = maps.Clone(project.Files)
			project.EntryPoints = slices.Clone(project.EntryPoints)
			for _, change := range result.Edits.Edit.DocumentChanges {
				if change.CreateFile != nil {
					name, err := s.DocumentName(ctx, change.CreateFile.URI)
					if err != nil {
						t.Fatal(err)
					}
					if _, exists := project.Files[name]; exists {
						t.Fatal("unexpected overwrite")
					}
					project.Files[name] = ""
					project.EntryPoints = append(project.EntryPoints, name)
				}
				if change.TextDocumentEdit != nil {
					document := change.TextDocumentEdit
					name, err := s.DocumentName(ctx, document.TextDocument.URI)
					if err != nil {
						t.Fatal(err)
					}
					project.Files[name] = applyFormattingCopy(t, project.Files[name], document.Edits)
				}
			}
			diagnostics, err := goya.Check(ctx, project, before.Environment, before.CompilerOptions)
			if err != nil || goya.HasTypeErrors(diagnostics) {
				t.Fatalf("moved catalog: %+v %v\n%v", diagnostics, err, project.Files)
			}
			if err := s.UpdateProject(ctx, project); err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateEdits(ctx, *result.Edits); !errors.Is(err, ErrContentModified) {
				t.Fatalf("old move proposal: %v", err)
			}
			if _, err := s.ApplyRefactor(ctx, RefactorSelection{Handle: choices.Handle}, args); !errors.Is(err, ErrContentModified) {
				t.Fatalf("old move selection: %v", err)
			}
		})
	}
}

func isUnsupportedMoveClient(err error) bool {
	var failure *Error
	return errors.As(err, &failure) && failure.Code == CodeUnsupportedClient
}

func TestMoveTargetReasonsAndReadonlyBoundaries(t *testing.T) {
	ctx := context.Background()
	text := "export const value = 1;\n"
	project := goya.Project{Files: map[string]string{"/main.ts": text}, EntryPoints: []string{"/main.ts"}}
	s, err := NewSession(ctx, SessionOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	end, _ := PositionAt(text, len(text)-1)
	span := protocol.Range{End: end}
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{libraryPrefix + "lib.es5.d.ts"} {
		result, err := s.RefactorEdits(ctx, "/main.ts", span, "Move to file", "Move to file", &RefactorArguments{TargetFile: target})
		if !errors.Is(err, ErrReadOnly) || result.Edits != nil {
			t.Fatalf("read-only target: %+v %v", result, err)
		}
	}
	rejected, err := s.RefactorEdits(ctx, "/main.ts", span, "Move to file", "Move to file", &RefactorArguments{TargetFile: "/target.txt"})
	if err != nil || rejected.Edits != nil || rejected.NotApplicableReason != "Cannot move to file, selected file is invalid" {
		t.Fatalf("target reason: %+v %v", rejected, err)
	}
	after, err := s.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected move changed catalog: %v", err)
	}
}

func TestMoveToStatementFreeTargetUsesActualSourceEOF(t *testing.T) {
	for _, target := range []string{"", "  ", "\n", "// keep\n", "// keep\r\n", "/* 😀\u2028keep\u2029 */", "\ufeff", "/* keep */"} {
		t.Run(fmt.Sprintf("%q", target), func(t *testing.T) {
			ctx := context.Background()
			text := "export const value = 1;\nvalue;\n"
			project := goya.Project{Files: map[string]string{"/main.ts": text, "/target.ts": target}, EntryPoints: []string{"/main.ts", "/target.ts"}}
			s, err := NewSession(ctx, SessionOptions{Project: project})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			before, _ := s.Snapshot(ctx)
			end, _ := PositionAt(text, len("export const value = 1;"))
			result, err := s.RefactorEdits(ctx, "/main.ts", protocol.Range{End: end}, "Move to file", "Move to file", &RefactorArguments{TargetFile: "/target.ts"})
			if err != nil || result.Edits == nil {
				t.Fatalf("move: %+v %v", result, err)
			}
			copy := project
			copy.Files = maps.Clone(project.Files)
			found := false
			eof, _ := PositionAt(target, len(target))
			for _, change := range result.Edits.Edit.DocumentChanges {
				if change.TextDocumentEdit == nil {
					t.Fatal("existing target produced a resource operation")
				}
				d := change.TextDocumentEdit
				name, err := s.DocumentName(ctx, d.TextDocument.URI)
				if err != nil {
					t.Fatal(err)
				}
				if name == "/target.ts" {
					found = true
					if len(d.Edits) != 1 || d.Edits[0].Range != (protocol.Range{Start: eof, End: eof}) {
						t.Fatalf("non-source EOF: %+v want %+v", d.Edits, eof)
					}
				}
				copy.Files[name] = applyFormattingCopy(t, copy.Files[name], d.Edits)
			}
			if !found || copy.Files["/target.ts"] != target+"\nexport const value = 1;\n" {
				t.Fatalf("target text: %q", copy.Files["/target.ts"])
			}
			if ds, err := goya.Check(ctx, copy, before.Environment, before.CompilerOptions); err != nil || goya.HasTypeErrors(ds) {
				t.Fatalf("moved copy: %+v %v", ds, err)
			}
			after, _ := s.Snapshot(ctx)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("move changed live catalog")
			}
		})
	}
}
