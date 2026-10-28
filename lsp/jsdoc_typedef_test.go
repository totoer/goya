package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp/protocol"
)

func TestTypedefReferenceFormattingErrorsRejectWholeProposalAndRecover(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-typedef-*-error.json", 6, "Token end is child end", "JSDoc type cannot be formatted")
}

func TestUnmatchedParamArrowOverlappingReferenceEditsRejectWholeProposalAndRecover(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-unmatched-param-arrow-own-error.json", 1, "Changes overlap", "changes overlap")
}

func TestAccessorCrossFileFixAllReferenceErrorRejectsWholeProposalAndRecovers(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-accessor-base-cross-file-many-all-error.json", 1, "_ChangeTracker.pushRaw", "accessor fix-all source file mismatch")
}

func testReferenceInvalidEditFailures(t *testing.T, pattern string, minimum int, oracleReason, nativeReason string) {
	t.Helper()
	paths, err := filepath.Glob("../testdata/lsp/oracle-errors/" + pattern)
	if err != nil || len(paths) < minimum {
		t.Fatalf("missing pinned formatting failures: %v %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := lspfixture.Load(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			var oracle struct {
				Code    int
				Message string
			}
			step := fixture.Steps[len(fixture.Steps)-1]
			if err := json.Unmarshal(step.ExpectedError, &oracle); err != nil || oracle.Code != 1 || !strings.Contains(oracle.Message, oracleReason) {
				t.Fatalf("unrecorded reference failure: %+v %v", oracle, err)
			}
			ctx := context.Background()
			s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: fixture.Project.EntryPoints, Files: fixture.Project.Files}, CompilerOptions: goya.CompileOptions(fixture.Project.Options)})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			text := fixture.Project.Files["/main.ts"]
			if err := s.OpenDocument(ctx, "/main.ts", 2, text); err != nil {
				t.Fatal(err)
			}
			before, err := s.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			paramsStep := step
			actionIndex := -1
			if source := step.ParamsFromCodeAction; source != nil {
				paramsStep = fixture.Steps[source.Step]
				actionIndex = source.Index
			}
			var params protocol.CodeActionParams
			if err := json.Unmarshal(paramsStep.Params, &params); err != nil {
				t.Fatal(err)
			}
			params.TextDocument.URI = "file:///main.ts"
			diagnostics, err := s.Diagnostics(ctx, "/main.ts", nil)
			if err != nil {
				t.Fatal(err)
			}
			params.Context.Diagnostics = diagnostics.Items
			result, err := s.CodeActions(ctx, "/main.ts", params.Range, params.Context, nil)
			partial := len(result.Actions) != 0
			if actionIndex >= 0 {
				if err != nil || actionIndex >= len(result.Actions) {
					t.Fatalf("missing action to resolve: %+v %v", result, err)
				}
				resolved, resolveErr := s.ResolveCodeAction(ctx, result.Actions[actionIndex])
				err = resolveErr
				partial = resolved.Edits != nil || resolved.Action.Edit != nil
			}
			if !errors.Is(err, &Error{Code: CodeInvalidEdit}) || partial || !strings.Contains(err.Error(), nativeReason) {
				t.Fatalf("unformattable proposal: %+v %v", result, err)
			}
			c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"codeAction": map[string]any{"resolveSupport": map[string]any{"properties": []string{"edit"}}}}}})
			if actionIndex >= 0 {
				c.send(t, "enumerate", "textDocument/codeAction", params)
				enumeration := c.read(t)
				var actions []protocol.CodeAction
				if err := json.Unmarshal(enumeration["result"], &actions); err != nil || actionIndex >= len(actions) {
					t.Fatalf("missing wire action: %s %v", enumeration["result"], err)
				}
				c.send(t, "invalid-typedef", "codeAction/resolve", actions[actionIndex])
			} else {
				c.send(t, "invalid-typedef", "textDocument/codeAction", params)
			}
			response := c.read(t)
			assertResponseCode(t, response, `"invalid-typedef"`, -32603)
			if len(response["result"]) > 0 {
				t.Fatal("partial proposal escaped")
			}
			c.send(t, "recover", "textDocument/hover", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: protocol.Position{}})
			response = c.read(t)
			if len(response["error"]) > 0 {
				t.Fatalf("subsequent request failed: %s", response["error"])
			}
			after, err := s.Snapshot(ctx)
			if err != nil || before.Generation != after.Generation || !maps.Equal(before.Project.Files, after.Project.Files) {
				t.Fatalf("failed proposal mutated session: %v", err)
			}
		})
	}
}
