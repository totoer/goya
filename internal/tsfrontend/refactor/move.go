package refactor

import (
	"fmt"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/diagnostics"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/parser"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
	"github.com/totoer/goya/lsp/protocol"
	"path"
	"slices"
)

const moveFileName = "Move to file"
const moveNewFileName = "Move to a new file"

var moveFileProvider = provider{name: moveFileName, kinds: []string{"refactor.move.file"}, available: func(r *request) []protocol.ApplicableRefactorInfo { return availableMove(r, false) }, edits: func(r *request, action string, args Arguments) (*EditInfo, error) {
	return editMove(r, action, args, false)
}}
var moveNewFileProvider = provider{name: moveNewFileName, kinds: []string{"refactor.move.newFile"}, available: func(r *request) []protocol.ApplicableRefactorInfo { return availableMove(r, true) }, edits: func(r *request, action string, args Arguments) (*EditInfo, error) {
	return editMove(r, action, args, true)
}}

func availableMove(r *request, newFile bool) []protocol.ApplicableRefactorInfo {
	if !newFile && !r.options.IncludeInteractive {
		return nil
	}
	selection := selectMoveStatements(r)
	if hideAutomaticMove(r) {
		return nil
	}
	name, kind, description := moveFileName, "refactor.move.file", diagnostics.Move_to_file
	if newFile {
		name, kind, description = moveNewFileName, "refactor.move.newFile", diagnostics.Move_to_a_new_file
	}
	action := protocol.RefactorActionInfo{Name: name, Description: description.Localize(locale.FromContext(r.ctx)), Kind: protocol.CodeActionKind(kind)}
	if r.preferences.AllowTextChangesInNewFiles.IsTrue() && selection != nil {
		span := core.NewTextRange(tokenStart(r, selection.all[0]), selection.all[len(selection.all)-1].End())
		converted := r.converters.ToLSPRange(r.file, span)
		action.Range = &protocol.Range{Start: protocol.Position(converted.Start), End: protocol.Position(converted.End)}
	} else if r.preferences.ProvideRefactorNotApplicableReason.IsTrue() {
		action.NotApplicableReason = diagnostics.Selection_is_not_a_valid_statement_or_statements.Localize(locale.FromContext(r.ctx))
	} else {
		return nil
	}
	return []protocol.ApplicableRefactorInfo{{Name: name, Description: action.Description, Actions: []protocol.RefactorActionInfo{action}}}
}
func moveNewFileNameForSelection(r *request, usage *moveUsage) string {
	name := ""
	for _, symbols := range []*moveSymbols{&usage.oldImportsFromTarget, &usage.moved} {
		for _, entry := range symbols.entries {
			if entry.symbol.Name != "default" {
				name = ast.SymbolName(entry.symbol)
			} else {
				for _, decl := range entry.symbol.Declarations {
					if candidate := decl.Name(); candidate != nil && ast.IsIdentifier(candidate) {
						name = candidate.Text()
						break
					}
				}
			}
			if name != "" {
				break
			}
		}
		if name != "" {
			break
		}
	}
	if name == "" {
		name = "newFile"
	}
	extension := tspath.GetAnyExtensionFromPath(r.file.FileName(), []string{tspath.ExtensionDts, tspath.ExtensionDmts, tspath.ExtensionDcts, tspath.ExtensionTs, tspath.ExtensionTsx, tspath.ExtensionMts, tspath.ExtensionCts}, false)
	directory := path.Dir(r.file.FileName())
	candidate := name
	for index := 1; ; index++ {
		file := path.Join(directory, candidate+extension)
		if _, exists := r.service.ReadFile(file); !exists {
			return file
		}
		candidate = fmt.Sprintf("%s.%d", name, index)
	}
}
func (r *request) registerMoveFile(file *ast.SourceFile) {
	r.lineMaps[file.FileName()] = lsconv.ComputeLSPLineStarts(file.Text())
}
func moveAddNewFileToConfig(r *request, t *change.Tracker, name string) error {
	configName := r.program.Options().ConfigFilePath
	if configName == "" {
		return nil
	}
	text, exists := r.service.ReadFile(configName)
	if !exists {
		return nil
	}
	file := parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: configName, Path: tspath.Path(configName)}, text, core.ScriptKindJSON)
	r.registerMoveFile(file)
	statements := file.Statements.Nodes
	if len(statements) == 0 || !ast.IsExpressionStatement(statements[0]) || !ast.IsObjectLiteralExpression(statements[0].Expression()) {
		return nil
	}
	for _, property := range statements[0].Expression().Properties() {
		if !ast.IsPropertyAssignment(property) || !ast.IsStringLiteral(property.Name()) || property.Name().Text() != "files" || !ast.IsArrayLiteralExpression(property.Initializer()) {
			continue
		}
		elements := property.Initializer().Elements()
		if len(elements) == 0 {
			return &change.InvalidEdits{Reason: "cannot append a moved file after an empty reference config files list"}
		}
		relative := tspath.GetRelativePathFromFile(configName, name, tspath.ComparePathsOptions{UseCaseSensitiveFileNames: r.service.UseCaseSensitiveFileNames(), CurrentDirectory: r.program.GetCurrentDirectory()})
		t.InsertNodeInListAfter(file, elements[len(elements)-1], t.NewStringLiteral(relative, ast.TokenFlagsNone), property.Initializer().AsArrayLiteralExpression().Elements)
		break
	}
	return nil
}
func editMove(r *request, action string, args Arguments, newFile bool) (*EditInfo, error) {
	expected := moveFileName
	if newFile {
		expected = moveNewFileName
	}
	if action != expected {
		return nil, &change.InvalidEdits{Reason: "invalid move action"}
	}
	selection := selectMoveStatements(r)
	if selection == nil {
		return nil, &change.InvalidEdits{Reason: "selection is not a valid statement or statements"}
	}
	c := r.types()
	usage, err := getMoveUsage(r, selection, nil)
	if err != nil {
		return nil, err
	}
	target := args.TargetFile
	if newFile {
		target = moveNewFileNameForSelection(r, usage)
	} else if target == "" {
		return nil, &change.InvalidEdits{Reason: "move to file requires interactive target arguments"}
	}
	if !tspath.HasTSFileExtension(target) && !tspath.HasJSFileExtension(target) {
		return &EditInfo{NotApplicableReason: diagnostics.Cannot_move_to_file_selected_file_is_invalid.Localize(locale.FromContext(r.ctx))}, nil
	}
	if !path.IsAbs(target) {
		target = path.Join(r.program.GetCurrentDirectory(), target)
	}
	target = path.Clean(target)
	targetSource := r.program.GetSourceFile(target)
	_, exists := r.service.ReadFile(target)
	future := !exists
	if exists && targetSource == nil {
		return &EditInfo{NotApplicableReason: diagnostics.Cannot_move_statements_to_the_selected_file.Localize(locale.FromContext(r.ctx))}, nil
	}
	if future {
		targetSource = parser.ParseSourceFile(ast.SourceFileParseOptions{FileName: target, Path: tspath.Path(target), ExternalModuleIndicatorOptions: ast.ExternalModuleIndicatorOptions{Force: r.file.ExternalModuleIndicator != nil}}, "", core.GetScriptKindFromFileName(target))
		r.registerMoveFile(targetSource)
	} else {
		usage, err = getMoveUsage(r, selection, moveExistingLocals(r, targetSource, selection.all))
		if err != nil {
			return nil, err
		}
	}
	view, err := r.service.ReferenceAutoImportView(r.file)
	if err != nil {
		return nil, err
	}
	t := r.tracker()
	t.ReferenceSourceContexts = true
	oldAdder := newMoveImportAdder(r, t, r.file, view, false)
	newAdder := newMoveImportAdder(r, t, targetSource, autoimport.ReferenceViewForFile(view, targetSource, future), future)
	quote := lsutil.GetQuotePreference(r.file, r.preferences)
	for _, entry := range usage.oldImportsFromTarget.entries {
		oldAdder.addMoved(entry.symbol, entry.typeOnly, target)
	}
	for _, statement := range r.file.Statements.Nodes {
		if slices.Contains(selection.all, statement) {
			continue
		}
		moveForEachImport(statement, func(node *ast.Node) {
			moveForEachImportAlias(node, func(decl *ast.Node) {
				if usage.unusedOldImports.has(decl.Symbol()) {
					oldAdder.removeImport(decl)
				}
			})
		})
	}
	oldAdder.write(quote)
	for _, group := range selection.ranges {
		t.DeleteReferenceNodeRangeExcludingEnd(r.file, group.first, group.afterLast)
	}
	moveUpdateConsumers(r, t, usage, target, quote)
	moveAddOldExports(r, t, usage)
	for _, entry := range usage.importsNeededByTarget.entries {
		symbol := c.SkipAlias(entry.symbol)
		if symbol == c.GetUnknownSymbol() {
			reference := entry.reference
			if reference == nil && len(entry.symbol.Declarations) > 0 {
				reference = ancestor(entry.symbol.Declarations[0], func(n *ast.Node) bool {
					return ast.IsImportDeclaration(n) || ast.IsImportEqualsDeclaration(n) || ast.IsRequireVariableStatement(n)
				})
			}
			if reference == nil {
				return nil, &change.InvalidEdits{Reason: "missing unresolved reference import"}
			}
			newAdder.addVerbatim(reference)
		} else if symbol.Parent == nil {
			newAdder.addModule(entry.symbol, entry.reference)
		} else {
			newAdder.addSymbol(symbol, entry.typeOnly, entry.reference)
		}
	}
	for _, entry := range usage.targetImportsFromOld.entries {
		newAdder.addMoved(entry.symbol, entry.typeOnly, r.file.FileName())
	}
	statements := []*ast.Node{}
	if future {
		for _, statement := range r.file.Statements.Nodes {
			if !movePrologue(statement) {
				break
			}
			statements = append(statements, statement)
		}
	}
	statements = append(statements, newAdder.write(quote)...)
	body := moveExportedBody(r, t, selection, usage)
	if future {
		if newAdder.hasFixes() {
			statements = append(statements, nil)
		}
		statements = append(statements, body...)
	} else if len(targetSource.Statements.Nodes) > 0 {
		moveToExistingFile(r, t, targetSource, body, selection)
	} else {
		t.InsertReferenceNodesAtEndOfFile(targetSource, body)
	}
	var newText string
	if future {
		newText = t.ReferencePrintNewFile(target, statements, r.file)
		if err := moveAddNewFileToConfig(r, t, target); err != nil {
			return nil, err
		}
	}
	result := finish(t)
	if future {
		result.Changes = append(result.Changes, ls.ReferenceFileCodeChanges{FileName: target, IsNewFile: true, TextChanges: []*lsproto.TextEdit{{Range: lsproto.Range{}, NewText: newText}}})
	}
	return result, r.ctx.Err()
}
