package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRefactorReferenceFailureRejectsWholeProposalAndRecovers(t *testing.T) {
	for _, item := range []struct{ file, reason string }{
		{"refactor-braces-inapplicable-error.json", "Expected applicable refactor info"},
		{"refactor-accessor-anonymous-error.json", "Cannot read properties of undefined"},
		{"refactor-parameters-extra-arguments-error.json", "Cannot read properties of undefined (reading 'name')"},
		{"refactor-move-overlap-error.json", "Changes overlap"},
		{"refactor-move-require-alias-error.json", "Should only get Alias here"},
	} {
		t.Run(item.file, func(t *testing.T) { testRefactorReferenceFailure(t, item.file, item.reason) })
	}
}
func testRefactorReferenceFailure(t *testing.T, name, oracleReason string) {
	t.Helper()
	file, err := os.Open("../testdata/lsp/oracle-errors/" + name)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := lspfixture.Load(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	step := fixture.Steps[len(fixture.Steps)-1]
	var oracle struct {
		Code    int
		Message string
	}
	if err := json.Unmarshal(step.ExpectedError, &oracle); err != nil || oracle.Code != 1 || !strings.Contains(oracle.Message, oracleReason) {
		t.Fatalf("missing recorded reference failure: %+v %v", oracle, err)
	}
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: fixture.Project.ConfigFile, Files: fixture.Project.Files, EntryPoints: fixture.Project.EntryPoints}, CompilerOptions: goya.CompileOptions(fixture.Project.Options)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		Arguments []json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(step.Params, &command); err != nil || len(command.Arguments) != 1 {
		t.Fatalf("recorded command: %+v %v", command, err)
	}
	var argument applyRefactorArgument
	if err := json.Unmarshal(command.Arguments[0], &argument); err != nil {
		t.Fatal(err)
	}
	position := protocol.Position{Line: argument.StartLine - 1, Character: argument.StartOffset - 1}
	end := protocol.Position{Line: argument.EndLine - 1, Character: argument.EndOffset - 1}
	result, err := s.RefactorEdits(ctx, "/main.ts", protocol.Range{Start: position, End: end}, argument.Refactor, argument.Action, nil)
	if !errors.Is(err, &Error{Code: CodeInvalidEdit}) || result.Edits != nil || result.Rename != nil {
		t.Fatalf("inapplicable proposal: %+v %v", result, err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	params := strings.ReplaceAll(string(step.Params), "{{ROOT_PATH}}/main.ts", "/main.ts")
	c.send(t, "inapplicable-refactor", "workspace/executeCommand", json.RawMessage(params))
	response := c.read(t)
	assertResponseCode(t, response, `"inapplicable-refactor"`, -32603)
	if len(response["result"]) > 0 {
		t.Fatal("partial refactor escaped")
	}
	c.send(t, "recover-refactor", "textDocument/hover", protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: "file:///main.ts"}, Position: position})
	response = c.read(t)
	if len(response["error"]) > 0 {
		t.Fatalf("subsequent request failed: %s", response["error"])
	}
	after, err := s.Snapshot(ctx)
	if err != nil || before.Generation != after.Generation || !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatalf("failed refactor mutated session: %v", err)
	}
}

func TestMoveNewFileRejectsEmptyHostSelectedConfigWithoutPartialProposal(t *testing.T) {
	// Host entry points can deliberately override an empty config files list.
	// This host-only shape is recorded through the pinned language-service API,
	// since ordinary TLS configuration would place that source in an inferred project.
	raw, err := os.ReadFile("../testdata/lsp/oracle-errors/host-move-empty-config-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ReferenceAPI      string                `json:"reference_api"`
		TypeScriptVersion string                `json:"typescript_version"`
		PackageSHA        string                `json:"typescript_package_sha256"`
		Project           lspfixture.Project    `json:"project"`
		Arguments         applyRefactorArgument `json:"arguments"`
		Error             string                `json:"error"`
	}
	if err := json.Unmarshal(raw, &records); err != nil || len(records) != 1 {
		t.Fatalf("source evidence: %v", err)
	}
	reference := records[0]
	if reference.ReferenceAPI != "TypeScript.LanguageService.getEditsForRefactor" || reference.TypeScriptVersion != "6.0.3" || reference.PackageSHA != "33cd0ee1beaa8c9e9d15a9da836c62ddea4c34a42d7c2d349dbc80d94165d22a" || !strings.Contains(reference.Error, "Debug Failure") || !strings.Contains(reference.Error, "addNewFileToTsconfig") {
		t.Fatal("missing pinned source failure")
	}
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: reference.Project.ConfigFile, Files: reference.Project.Files, EntryPoints: reference.Project.EntryPoints}, CompilerOptions: goya.CompileOptions(reference.Project.Options)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, _ := s.Snapshot(ctx)
	arg := reference.Arguments
	span := protocol.Range{Start: protocol.Position{Line: arg.StartLine - 1, Character: arg.StartOffset - 1}, End: protocol.Position{Line: arg.EndLine - 1, Character: arg.EndOffset - 1}}
	result, err := s.RefactorEdits(ctx, "/main.ts", span, arg.Refactor, arg.Action, nil)
	if !errors.Is(err, &Error{Code: CodeInvalidEdit}) || result.Edits != nil || result.Rename != nil {
		t.Fatalf("partial proposal: %+v %v", result, err)
	}
	c := startTestConnection(t, nil, 8, 8, testClientSetup{session: s})
	arg.File = "/main.ts"
	encoded, _ := json.Marshal(arg)
	c.send(t, "empty-config", "workspace/executeCommand", protocol.ExecuteCommandParams{Command: applyRefactoringCommand, Arguments: []json.RawMessage{encoded}})
	assertResponseCode(t, c.read(t), `"empty-config"`, -32603)
	c.send(t, "recover", "goya/readDocument", map[string]any{"uri": "file:///main.ts"})
	response := c.read(t)
	if string(response["id"]) != `"recover"` || len(response["error"]) > 0 {
		t.Fatalf("failure poisoned connection: %+v", response)
	}
	after, _ := s.Snapshot(ctx)
	if before.Generation != after.Generation || !maps.Equal(before.Project.Files, after.Project.Files) {
		t.Fatal("failed move changed source")
	}
	c.finish(t)
}
