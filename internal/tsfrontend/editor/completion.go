package editor

import (
	"context"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/nodebuilder"
	"github.com/totoer/goya/internal/tsfrontend/upstream/printer"
	"github.com/totoer/goya/internal/tsfrontend/upstream/scanner"
)

// CompleteMembers fills reference editor behavior missing from the native LSP
// presentation. Compiler ASTs are used only within the current service lease.
func CompleteMembers(ctx context.Context, service *ls.LanguageService, file *ast.SourceFile, position int, list *ls.CompletionList, insertText, snippets bool) error {
	if list == nil {
		return nil
	}
	if list.JSDoc {
		return nil
	}
	for _, entry := range list.Items {
		if entry.CompletionItem == nil || entry.Data == nil {
			continue
		}
		item := entry.CompletionItem
		if item.Data.Source == ls.SourceTypeOnlyAlias {
			if item.SortText != nil {
				sort := "\uffff" + *item.SortText
				item.SortText = &sort
			}
		}
		if entry.Data.AutoImport == nil {
			continue
		}
		if item.SortText != nil && !item.Data.IsImportStatementCompletion {
			sort := "\uffff" + *item.SortText
			item.SortText = &sort
		}
		item.LabelDetails = nil
		if entry.ReferenceUnresolvedAutoImport {
			item.Detail = nil
		} else {
			detail := item.Data.AutoImport.ModuleSpecifier
			item.Detail = &detail
		}
	}
	prefs := service.UserPreferences()
	object := list.Kind == ls.CompletionKindObjectPropertyDeclaration && list.Location != nil && ast.FindAncestor(list.Location, ast.IsObjectLiteralExpression) != nil
	class := list.Kind == ls.CompletionKindMemberLike
	if !object && !class {
		sortCompletions(list)
		return nil
	}
	ch, done := service.GetProgram().GetTypeCheckerForFile(ctx, file)
	defer done()
	var extra []*ls.CompletionItem
	for _, entry := range list.Items {
		if entry.Symbol == nil || entry.CompletionItem == nil {
			continue
		}
		item := entry.CompletionItem
		if object && prefs.IncludeCompletionsWithObjectLiteralMethodSnippets.IsTrue() && insertText {
			base := string(ls.SortTextLocationPriority)
			if item.SortText != nil {
				base = *item.SortText
			}
			name := strings.TrimSuffix(item.Label, "?")
			sort := base + "\x00" + name + "\x00"
			item.SortText = &sort
			method, detail := objectLiteralMethod(ctx, service, ch, file, entry.Symbol, list.Location, name, snippets)
			if method != "" {
				copy := *item
				copy.Label = name
				copy.InsertText = &method
				copy.FilterText = &method
				copy.LabelDetails = &lsproto.CompletionItemLabelDetails{Detail: &detail}
				if !lsproto.GetClientCapabilities(ctx).TextDocument.Completion.CompletionItem.LabelDetailsSupport {
					copy.Label += detail
					copy.LabelDetails = nil
				}
				if snippets {
					copy.InsertTextFormat = new(lsproto.InsertTextFormatSnippet)
				}
				snippetSort := sort + "1"
				copy.SortText = &snippetSort
				copy.Data = &lsproto.CompletionItemData{FileName: file.FileName(), Position: int32(position), Name: copy.Label}
				// In a partially typed literal, replace the same word as the base item.
				if item.TextEdit != nil {
					if item.TextEdit.TextEdit != nil {
						v := *item.TextEdit.TextEdit
						v.NewText = method
						copy.TextEdit = &lsproto.TextEditOrInsertReplaceEdit{TextEdit: &v}
					}
					if item.TextEdit.InsertReplaceEdit != nil {
						v := *item.TextEdit.InsertReplaceEdit
						v.NewText = method
						copy.TextEdit = &lsproto.TextEditOrInsertReplaceEdit{InsertReplaceEdit: &v}
					}
				}
				extra = append(extra, &ls.CompletionItem{CompletionItem: &copy, Symbol: entry.Symbol})
			}
		}
		if class && prefs.IncludeCompletionsWithClassMemberSnippets.IsTrue() && insertText {
			text, edits, err := service.EmbeddedClassMemberCompletion(ctx, ch, file, entry.Symbol, list.Location, snippets)
			if err != nil {
				return err
			}
			if text != "" {
				item.FilterText = &text
				if item.TextEdit != nil {
					if item.TextEdit.TextEdit != nil {
						item.TextEdit.TextEdit.NewText = text
					}
					if item.TextEdit.InsertReplaceEdit != nil {
						item.TextEdit.InsertReplaceEdit.NewText = text
					}
				} else {
					item.InsertText = &text
				}
				if snippets {
					item.InsertTextFormat = new(lsproto.InsertTextFormatSnippet)
				}
				if len(edits) > 0 {
					item.AdditionalTextEdits = &edits
				}
			}
		}
	}
	list.Items = append(list.Items, extra...)
	sortCompletions(list)
	return nil
}

func sortCompletions(list *ls.CompletionList) {
	// TypeScript's editor list uses the English UI collator, so lowercase
	// keywords sort alongside capitalized standard-library names.
	order := collate.New(language.English, collate.Numeric)
	slices.SortStableFunc(list.Items, func(a, b *ls.CompletionItem) int {
		if a.SortText != nil && b.SortText != nil {
			// TLS adds the UI de-prioritization prefix after the compiler has
			// ordered the response. Keep that response order here as well.
			if compared := order.CompareString(strings.TrimPrefix(*a.SortText, "\uffff"), strings.TrimPrefix(*b.SortText, "\uffff")); compared != 0 {
				return compared
			}
		}
		return order.CompareString(a.Label, b.Label)
	})
}

func objectLiteralMethod(ctx context.Context, service *ls.LanguageService, ch *checker.Checker, file *ast.SourceFile, symbol *ast.Symbol, location *ast.Node, name string, snippets bool) (string, string) {
	if symbol.Flags&(ast.SymbolFlagsProperty|ast.SymbolFlagsMethod) == 0 {
		return "", ""
	}
	enclosing := ast.FindAncestor(location, ast.IsObjectLiteralExpression)
	if enclosing == nil {
		return "", ""
	}
	t := ch.GetWidenedType(ch.GetTypeOfSymbolAtLocation(symbol, enclosing))
	if t.IsUnion() {
		var functions []*checker.Type
		for _, part := range t.Types() {
			if len(ch.GetSignaturesOfType(part, checker.SignatureKindCall)) > 0 {
				functions = append(functions, part)
			}
		}
		if len(functions) != 1 {
			return "", ""
		}
		t = functions[0]
	}
	if len(ch.GetSignaturesOfType(t, checker.SignatureKindCall)) != 1 {
		return "", ""
	}
	typeNode := ch.TypeToTypeNode(t, enclosing, nodebuilder.FlagsOmitThisParameter, nil)
	if typeNode == nil || typeNode.Kind != ast.KindFunctionType {
		return "", ""
	}
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	var parameters []*ast.Node
	for _, param := range typeNode.ParameterList().Nodes {
		p := param.AsParameterDeclaration()
		parameters = append(parameters, factory.NewParameterDeclaration(nil, p.DotDotDotToken, p.Name(), nil, nil, p.Initializer))
	}
	var statements []*ast.Node
	if snippets {
		statements = append(statements, factory.NewExpressionStatement(factory.NewIdentifier("__goya_completion_tabstop__")))
	}
	body := factory.NewBlock(factory.NewNodeList(statements), true)
	property := factory.NewIdentifier(name)
	if !scanner.IsIdentifierText(name, core.LanguageVariantStandard) {
		property = factory.NewStringLiteral(name, ast.TokenFlagsNone)
	}
	method := factory.NewMethodDeclaration(nil, nil, property, nil, nil, factory.NewNodeList(parameters), nil, nil, body)
	text := service.EmbeddedPrintCompletionNode(ctx, method, file)
	if snippets {
		text = strings.Replace(text, "__goya_completion_tabstop__;", "$0", 1)
	}
	text += ","
	signature := factory.NewMethodSignatureDeclaration(nil, factory.NewIdentifier(""), nil, nil, factory.NewNodeList(parameters), nil)
	print := printer.NewPrinter(printer.PrinterOptions{RemoveComments: true}, printer.PrintHandlers{}, nil)
	detail := strings.TrimSuffix(strings.TrimSpace(print.Emit(signature, file)), ";")
	return text, detail
}
