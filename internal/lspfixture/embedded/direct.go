// Package embedded runs shared fixtures against public direct and in-memory
// wire APIs. No oracle executable, editor or OS/network listener is involved.
package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func OptionsForFixture(f lspfixture.Fixture, rootURI string) (lsp.SessionOptions, error) {
	files := maps.Clone(f.Project.Files)
	if files == nil {
		files = map[string]string{}
	}
	for name, text := range f.Project.Declarations {
		if _, ok := files[name]; ok {
			return lsp.SessionOptions{}, fmt.Errorf("fixture declaration overlaps source: %s", name)
		}
		files[name] = text
	}
	root, err := url.Parse(rootURI)
	if err != nil || root.Scheme == "" {
		return lsp.SessionOptions{}, fmt.Errorf("invalid fixture root URI")
	}
	mappings := map[string]protocol.DocumentURI{}
	for name := range files {
		uri := *root
		uri.Path = root.Path + "/" + name
		uri.RawPath = ""
		if len(name) > 0 && name[0] == '/' {
			uri.Path = root.Path + name
		}
		mappings[name] = protocol.DocumentURI(uri.String())
	}
	// The fixture host owns the virtual namespace, including future resources
	// named in file-operation requests. The server does not infer opaque URI IDs.
	for _, step := range f.Steps {
		var params protocol.RenameFilesParams
		switch step.Method {
		case "workspace/willRenameFiles":
			if err := json.Unmarshal(step.Params, &params); err != nil {
				return lsp.SessionOptions{}, err
			}
		case "workspace/executeCommand":
			var command protocol.ExecuteCommandParams
			if err := json.Unmarshal(step.Params, &command); err != nil {
				return lsp.SessionOptions{}, err
			}
			if command.Command != "_typescript.applyRenameFile" || len(command.Arguments) == 0 {
				continue
			}
			var rename struct {
				SourceURI protocol.DocumentURI `json:"sourceUri"`
				TargetURI protocol.DocumentURI `json:"targetUri"`
			}
			if err := json.Unmarshal(command.Arguments[0], &rename); err != nil {
				return lsp.SessionOptions{}, err
			}
			params.Files = []protocol.FileRename{{OldURI: rename.SourceURI, NewURI: rename.TargetURI}}
		default:
			continue
		}
		for _, file := range params.Files {
			for _, uri := range []protocol.DocumentURI{file.OldURI, file.NewURI} {
				raw := string(uri)
				if !strings.HasPrefix(raw, "{{ROOT_URI}}/") {
					continue
				}
				name, err := url.PathUnescape(strings.TrimPrefix(raw, "{{ROOT_URI}}"))
				if err != nil {
					return lsp.SessionOptions{}, err
				}
				mapped := *root
				mapped.Path = root.Path + name
				mapped.RawPath = ""
				mappings[name] = protocol.DocumentURI(mapped.String())
			}
		}
	}
	// Future refactor resources belong to the fixture host's URI namespace.
	for _, step := range f.Steps {
		for _, request := range step.ExpectedClientRequests {
			if request.Method != "workspace/applyEdit" {
				continue
			}
			var params protocol.ApplyWorkspaceEditParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				return lsp.SessionOptions{}, err
			}
			for uri := range params.Edit.Changes {
				raw := string(uri)
				if !strings.HasPrefix(raw, "{{ROOT_URI}}/") {
					continue
				}
				name, err := url.PathUnescape(strings.TrimPrefix(raw, "{{ROOT_URI}}"))
				if err != nil {
					return lsp.SessionOptions{}, err
				}
				mapped := *root
				mapped.Path = root.Path + name
				mapped.RawPath = ""
				mappings[name] = protocol.DocumentURI(mapped.String())
			}
		}
	}
	return lsp.SessionOptions{Project: goya.Project{ConfigFile: f.Project.ConfigFile, Files: files, EntryPoints: f.Project.EntryPoints}, CompilerOptions: goya.CompileOptions(f.Project.Options), DocumentURIs: mappings}, nil
}

func defaultInitialization(initialization *lspfixture.Initialization) lspfixture.Initialization {
	if initialization == nil {
		return lspfixture.Initialization{Capabilities: map[string]any{"textDocument": map[string]any{"hover": map[string]any{"contentFormat": []string{"markdown"}}}}}
	}
	copy := *initialization
	if copy.Capabilities == nil {
		copy.Capabilities = map[string]any{}
	}
	return copy
}

type Direct struct {
	clientMu            sync.Mutex
	clientRequests      []lspfixture.ClientRequest
	clientResponses     map[string]json.RawMessage
	codeActionCaps      lsp.CodeActionCapabilities
	session             *lsp.Session
	hoverOptions        lsp.HoverOptions
	diagnosticOptions   lsp.DiagnosticOptions
	completionCaps      lsp.CompletionCapabilities
	definitionLinks     bool
	hierarchicalSymbols bool
	codeLensSupport     bool
}

func NewDirect(ctx context.Context, options lsp.SessionOptions, initialization *lspfixture.Initialization) (*Direct, error) {
	init := defaultInitialization(initialization)
	data, err := json.Marshal(init.Options)
	if err != nil {
		return nil, err
	}
	var settings struct {
		Locale                       string `json:"locale"`
		HostInfo                     string `json:"hostInfo"`
		CompletionDisableFilterText  bool   `json:"completionDisableFilterText"`
		SupportsHoverVerbosity       bool   `json:"supportsHoverVerbosity"`
		SupportsMoveToFileCodeAction bool   `json:"supportsMoveToFileCodeAction"`
		Preferences                  *struct {
			lsp.EditorPreferences
			DisableSuggestions *bool `json:"disableSuggestions"`
		} `json:"preferences"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}
	options.EditorOptions.Locale = settings.Locale
	options.EditorOptions.HostInfo = settings.HostInfo
	options.EditorOptions.CompletionDisableFilterText = settings.CompletionDisableFilterText
	if settings.Preferences != nil && settings.Preferences.DisableSuggestions != nil {
		options.EditorOptions.Diagnostics.DisableSuggestions = *settings.Preferences.DisableSuggestions
	}
	if settings.Preferences != nil {
		options.EditorOptions.Preferences = settings.Preferences.EditorPreferences
	}
	capData, err := json.Marshal(init.Capabilities)
	if err != nil {
		return nil, err
	}
	var completionWire struct {
		Workspace struct {
			Configuration bool `json:"configuration"`
		} `json:"workspace"`
		TextDocument struct {
			CodeAction     lsp.CodeActionCapabilities `json:"codeAction"`
			CodeLens       json.RawMessage            `json:"codeLens"`
			DocumentSymbol struct {
				Hierarchical bool `json:"hierarchicalDocumentSymbolSupport"`
			} `json:"documentSymbol"`
			Definition struct {
				LinkSupport bool `json:"linkSupport"`
			} `json:"definition"`
			Completion struct {
				CompletionItem lsp.CompletionCapabilities `json:"completionItem"`
			} `json:"completion"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(capData, &completionWire); err != nil {
		return nil, err
	}
	completionWire.TextDocument.CodeAction.SupportsMoveToFileCodeAction = settings.SupportsMoveToFileCodeAction
	capabilities, err := fixtureDiagnosticCapabilities(init.Capabilities)
	if err != nil {
		return nil, err
	}

	if completionWire.Workspace.Configuration {
		options.FormattingOptionsProvider = func(ctx context.Context, uri protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data := init.ClientConfiguration[string(uri)]["formattingOptions"]
			if len(data) == 0 || string(data) == "null" {
				return nil, nil
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(data, &fields) != nil {
				return nil, nil
			}
			var result protocol.DocumentFormattingOptions
			if raw, ok := fields["tabSize"]; ok && string(raw) != "null" {
				var value uint32
				if json.Unmarshal(raw, &value) == nil {
					result.TabSize = &value
				}
			}
			if raw, ok := fields["insertSpaces"]; ok && string(raw) != "null" {
				var value bool
				if json.Unmarshal(raw, &value) == nil {
					result.InsertSpaces = &value
				}
			}
			return &result, nil
		}
	}
	s, err := lsp.NewSession(ctx, options)
	if err != nil {
		return nil, err
	}
	return &Direct{session: s, clientResponses: init.ClientResponses, codeActionCaps: completionWire.TextDocument.CodeAction, codeLensSupport: len(completionWire.TextDocument.CodeLens) > 0 && string(completionWire.TextDocument.CodeLens) != "null", hoverOptions: lsp.HoverOptions{SupportsVerbosity: settings.SupportsHoverVerbosity}, diagnosticOptions: lsp.DiagnosticOptions{Capabilities: &capabilities}, completionCaps: completionWire.TextDocument.Completion.CompletionItem, hierarchicalSymbols: completionWire.TextDocument.DocumentSymbol.Hierarchical, definitionLinks: completionWire.TextDocument.Definition.LinkSupport}, nil
}
func (d *Direct) Session() *lsp.Session                { return d.session }
func (d *Direct) TypeScriptVersion() string            { return goya.TypeScriptVersion() }
func (d *Direct) SourceStringCompilationEnabled() bool { return false }
func (d *Direct) Close() error                         { return d.session.Close() }
func (d *Direct) Notify(ctx context.Context, method string, params json.RawMessage) error {
	switch method {
	case "textDocument/didOpen":
		var p protocol.DidOpenTextDocumentParams
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		if p.TextDocument.LanguageID != "typescript" && p.TextDocument.LanguageID != "typescriptreact" {
			return &lsp.Error{Code: lsp.CodeInvalidDocument, Message: "only TypeScript/TSX documents are supported"}
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return err
		}
		return d.session.OpenDocument(ctx, name, p.TextDocument.Version, p.TextDocument.Text)
	case "textDocument/didChange":
		var p protocol.DidChangeTextDocumentParams
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return err
		}
		return d.session.ChangeDocument(ctx, name, p.TextDocument.Version, p.ContentChanges)
	case "textDocument/didSave":
		var p protocol.DidSaveTextDocumentParams
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return err
		}
		return d.session.SaveDocument(ctx, name)
	case "textDocument/didClose":
		var p protocol.DidCloseTextDocumentParams
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return err
		}
		return d.session.CloseDocument(ctx, name)
	case "workspace/didChangeWatchedFiles":
		var p protocol.DidChangeWatchedFilesParams
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		return d.session.NotifyFilesChanged(ctx, p.Changes)
	case "workspace/didChangeConfiguration":
		var p struct {
			Settings lsp.WorkspaceConfiguration `json:"settings"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return err
		}
		return d.session.Configure(ctx, p.Settings)
	default:
		return &lsp.ResponseError{Code: -32601, Message: "unsupported direct fixture notification: " + method}
	}
}
func (d *Direct) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "textDocument/codeAction":
		var p protocol.CodeActionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal([]protocol.CodeAction{})
		}
		result, err := d.session.CodeActions(ctx, name, p.Range, p.Context, &lsp.CodeActionRequestOptions{Capabilities: &d.codeActionCaps, DiagnosticCapabilities: d.diagnosticOptions.Capabilities})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Actions)
	case "codeAction/resolve":
		var item protocol.CodeAction
		if err := json.Unmarshal(params, &item); err != nil {
			return nil, err
		}
		if d.codeActionCaps.ResolveSupport == nil {
			return json.Marshal(item)
		}
		result, err := d.session.ResolveCodeAction(ctx, item)
		if err != nil {
			return nil, err
		}
		// Model the connection's advertised-property projection, keeping the direct
		// API's complete resolved result available to ordinary SDK consumers.
		raw, _ := json.Marshal(item)
		var before map[string]json.RawMessage
		_ = json.Unmarshal(raw, &before)
		raw, _ = json.Marshal(result.Action)
		var after map[string]json.RawMessage
		_ = json.Unmarshal(raw, &after)
		for _, key := range d.codeActionCaps.ResolveSupport.Properties {
			if value, ok := after[key]; ok {
				before[key] = value
			}
		}
		return json.Marshal(before)

	case "textDocument/codeLens":
		var p struct {
			TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if !d.codeLensSupport || doc.Version == nil {
			return json.Marshal([]protocol.CodeLens{})
		}
		result, err := d.session.CodeLenses(ctx, name)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Lenses)
	case "codeLens/resolve":
		var item protocol.CodeLens
		if err := json.Unmarshal(params, &item); err != nil {
			return nil, err
		}
		result, err := d.session.ResolveCodeLens(ctx, item)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Lens)
	case "textDocument/formatting", "textDocument/rangeFormatting":
		var p struct {
			TextDocument protocol.TextDocumentIdentifier
			Range        *protocol.Range
			Options      *protocol.FormattingOptions
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			if method == "textDocument/rangeFormatting" {
				return json.Marshal([]protocol.TextEdit{})
			}
			return nil, &lsp.ResponseError{Code: -32603, Message: fmt.Sprintf("Request textDocument/formatting failed with message: The document should be opened for formatting', file: %s", p.TextDocument.URI)}
		}
		if method == "textDocument/formatting" {
			p.Range = nil
		}
		result, err := d.session.Formatting(ctx, name, p.Range, p.Options)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.TextEdits)
	case "textDocument/inlayHint":
		var p protocol.InlayHintParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal([]protocol.InlayHint{})
		}
		r, err := d.session.InlayHints(ctx, name, p.Range)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r.Hints)
	case "textDocument/semanticTokens/full", "textDocument/semanticTokens/range":
		var p struct {
			TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
			Range        *protocol.Range                 `json:"range"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal(protocol.SemanticTokens{Data: []uint32{}})
		}
		span := p.Range
		if method == "textDocument/semanticTokens/full" {
			span = nil
		}
		r, err := d.session.SemanticTokens(ctx, name, span)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r.Tokens)
	case "textDocument/prepareCallHierarchy":
		var p protocol.TextDocumentPositionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal(nil)
		}
		r, err := d.session.PrepareCallHierarchy(ctx, name, p.Position)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r.Items)
	case "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls":
		var p protocol.CallHierarchyCallsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if method == "callHierarchy/incomingCalls" {
			r, err := d.session.IncomingCalls(ctx, p.Item)
			if err != nil {
				return nil, err
			}
			return json.Marshal(r.Calls)
		}
		r, err := d.session.OutgoingCalls(ctx, p.Item)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r.Calls)
	case "textDocument/foldingRange", "textDocument/selectionRange", "textDocument/linkedEditingRange":
		var p struct {
			TextDocument protocol.TextDocumentIdentifier `json:"textDocument"`
			Position     protocol.Position               `json:"position"`
			Positions    []protocol.Position             `json:"positions"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			if method == "textDocument/foldingRange" {
				return nil, &lsp.ResponseError{Code: -32603, Message: "Request textDocument/foldingRange failed with message: The document should be opened for foldingRanges', file: " + string(p.TextDocument.URI)}
			}
			return json.Marshal(nil)
		}
		switch method {
		case "textDocument/foldingRange":
			r, err := d.session.FoldingRanges(ctx, name)
			if err != nil {
				return nil, err
			}
			return json.Marshal(r.Ranges)
		case "textDocument/selectionRange":
			r, err := d.session.SelectionRanges(ctx, name, p.Positions)
			if err != nil {
				return nil, err
			}
			return json.Marshal(r.Ranges)
		default:
			r, err := d.session.LinkedEditingRanges(ctx, name, p.Position)
			if err != nil {
				return nil, err
			}
			return json.Marshal(r.Ranges)
		}
	case "textDocument/documentSymbol":
		var p protocol.DocumentSymbolParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal([]protocol.DocumentSymbol{})
		}
		result, err := d.session.DocumentSymbols(ctx, name)
		if err != nil {
			return nil, err
		}
		if d.hierarchicalSymbols {
			return json.Marshal(result.Symbols)
		}
		return json.Marshal(result.Flat())
	case "workspace/symbol":
		var p protocol.WorkspaceSymbolParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		result, err := d.session.WorkspaceSymbols(ctx, p.Query)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Symbols)
	case "textDocument/prepareRename", "textDocument/rename":
		var p protocol.RenameParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		doc, err := d.session.ReadDocument(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		if doc.Version == nil {
			return json.Marshal(nil)
		}
		if method == "textDocument/prepareRename" {
			result, err := d.session.PrepareRename(ctx, name, p.Position)
			var unavailable *lsp.RenameUnavailableError
			if errors.As(err, &unavailable) {
				return nil, &lsp.ResponseError{Code: -32603, Message: "Request textDocument/prepareRename failed with message: " + unavailable.Reason}
			}
			if err != nil {
				return nil, err
			}
			return json.Marshal(result.Range)
		}
		result, err := d.session.Rename(ctx, name, p.Position, p.NewName)
		if err != nil {
			return nil, err
		}
		if result.Edits == nil {
			return json.Marshal(nil)
		}
		return json.Marshal(result.Edits.Edit)
	case "textDocument/references":
		var p protocol.ReferenceParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		result, err := d.session.References(ctx, name, p.Position, &lsp.ReferenceOptions{IncludeDeclaration: p.Context.IncludeDeclaration})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Locations)
	case "textDocument/documentHighlight":
		var p protocol.TextDocumentPositionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		result, err := d.session.DocumentHighlights(ctx, name, p.Position)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Highlights)
	case "workspace/willRenameFiles":
		var renameParams protocol.RenameFilesParams
		if err := json.Unmarshal(params, &renameParams); err != nil {
			return nil, err
		}
		renames := make([]lsp.FileRename, 0, len(renameParams.Files))
		for _, file := range renameParams.Files {
			oldName, err := d.session.DocumentName(ctx, file.OldURI)
			if err != nil {
				return nil, err
			}
			newName, err := d.session.DocumentName(ctx, file.NewURI)
			if err != nil {
				return nil, err
			}
			renames = append(renames, lsp.FileRename{OldName: oldName, NewName: newName})
		}
		result, err := d.session.FileRenameEdits(ctx, renames)
		if err != nil {
			return nil, err
		}
		if result.Edits == nil {
			return json.Marshal(protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}})
		}
		return json.Marshal(result.Edits.Edit)
	case "workspace/executeCommand":
		var p protocol.ExecuteCommandParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if p.Command == "_typescript.applyCodeActionCommand" || p.Command == "_typescript.applyFixAllCodeAction" {
			var commands []json.RawMessage
			if p.Command == "_typescript.applyCodeActionCommand" {
				var arg struct {
					Action struct {
						Commands []json.RawMessage `json:"commands"`
					} `json:"action"`
				}
				if len(p.Arguments) == 0 {
					return nil, fmt.Errorf("missing action")
				}
				if err := json.Unmarshal(p.Arguments[0], &arg); err != nil {
					return nil, err
				}
				commands = arg.Action.Commands
			}
			result, err := d.session.CodeActionCommands(ctx, commands)
			if err != nil {
				return nil, err
			}
			if p.Command == "_typescript.applyFixAllCodeAction" {
				return json.RawMessage("null"), nil
			}
			return json.Marshal(result)
		}
		if p.Command == "_typescript.organizeImports" || p.Command == "_typescript.applyRenameFile" || p.Command == "_typescript.applyCompletionCodeAction" {
			return d.compatibilityCommand(ctx, p)
		}
		if p.Command == "_typescript.applyRefactoring" && len(p.Arguments) > 0 {
			return d.refactorCommand(ctx, p.Arguments[0])
		}
		if p.Command != "_typescript.goToSourceDefinition" || len(p.Arguments) < 2 {
			return nil, fmt.Errorf("unsupported direct fixture command %q", p.Command)
		}
		var uri protocol.DocumentURI
		var position protocol.Position
		if err := json.Unmarshal(p.Arguments[0], &uri); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(p.Arguments[1], &position); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, uri)
		if err != nil {
			return nil, err
		}
		result, err := d.session.SourceDefinition(ctx, name, position)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Locations())
	case "textDocument/definition", "textDocument/typeDefinition", "textDocument/implementation":
		var p protocol.TextDocumentPositionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		var result lsp.NavigationResult
		switch method {
		case "textDocument/definition":
			result, err = d.session.Definition(ctx, name, p.Position)
		case "textDocument/typeDefinition":
			result, err = d.session.TypeDefinition(ctx, name, p.Position)
		case "textDocument/implementation":
			result, err = d.session.Implementation(ctx, name, p.Position)
		}
		if err != nil {
			return nil, err
		}
		if method == "textDocument/definition" && d.definitionLinks {
			return json.Marshal(result.Targets)
		}
		return json.Marshal(result.Locations())
	case "textDocument/signatureHelp":
		var p protocol.SignatureHelpParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		result, err := d.session.SignatureHelp(ctx, name, p.Position, &lsp.SignatureHelpRequestOptions{Context: p.Context})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Help)
	case "textDocument/completion":
		var p protocol.CompletionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		result, err := d.session.Completion(ctx, name, p.Position, &lsp.CompletionRequestOptions{Context: p.Context, Capabilities: &d.completionCaps})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.List)
	case "completionItem/resolve":
		var item protocol.CompletionItem
		if err := json.Unmarshal(params, &item); err != nil {
			return nil, err
		}
		result, err := d.session.ResolveCompletion(ctx, item)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Item)
	case "textDocument/diagnostic":
		var p protocol.DocumentDiagnosticParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		result, err := d.session.Diagnostics(ctx, name, &d.diagnosticOptions)
		if err != nil {
			return nil, err
		}
		return json.Marshal(protocol.FullDocumentDiagnosticReport{Kind: "full", Items: result.Items})
	case "textDocument/hover":
		var p protocol.HoverParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		name, err := d.session.DocumentName(ctx, p.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		options := d.hoverOptions
		if p.VerbosityLevel != nil {
			options.VerbosityLevel = *p.VerbosityLevel
		}
		result, err := d.session.Hover(ctx, name, p.Position, &options)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result.Hover)
	case "goya/readDocument":
		var p protocol.ReadDocumentParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		result, err := d.session.ReadDocument(ctx, p.URI)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, &lsp.ResponseError{Code: -32601, Message: "unsupported direct fixture request: " + method}
	}
}

func fixtureDiagnosticCapabilities(input map[string]any) (lsp.DiagnosticCapabilities, error) {
	type wireCaps struct {
		RelatedInformation bool `json:"relatedInformation"`
		TagSupport         *struct {
			ValueSet []protocol.DiagnosticTag `json:"valueSet"`
		} `json:"tagSupport"`
	}
	var wire struct {
		TextDocument *struct {
			Diagnostic         *wireCaps `json:"diagnostic"`
			PublishDiagnostics *wireCaps `json:"publishDiagnostics"`
		} `json:"textDocument"`
	}
	data, err := json.Marshal(input)
	if err != nil {
		return lsp.DiagnosticCapabilities{}, err
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return lsp.DiagnosticCapabilities{}, err
	}
	result := lsp.DiagnosticCapabilities{RelatedInformation: true}
	if wire.TextDocument != nil {
		caps := wire.TextDocument.Diagnostic
		if caps == nil {
			caps = wire.TextDocument.PublishDiagnostics
		}
		if caps != nil {
			// TLS preserves related locations with basic diagnostic capabilities.
			if caps.TagSupport != nil {
				result.TagSupport = append([]protocol.DiagnosticTag{}, caps.TagSupport.ValueSet...)
			}
		}
	}
	return result, nil
}

func (d *Direct) ClientRequests() []lspfixture.ClientRequest {
	d.clientMu.Lock()
	defer d.clientMu.Unlock()
	result := slices.Clone(d.clientRequests)
	for i := range result {
		result[i].Params = slices.Clone(result[i].Params)
	}
	return result
}
func (d *Direct) refactorCommand(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var argument struct {
		File                 string `json:"file"`
		StartLine            uint32 `json:"startLine"`
		StartOffset          uint32 `json:"startOffset"`
		EndLine              uint32 `json:"endLine"`
		EndOffset            uint32 `json:"endOffset"`
		Refactor             string `json:"refactor"`
		Action               string `json:"action"`
		InteractiveArguments *struct {
			TargetFile string `json:"targetFile"`
		} `json:"interactiveRefactorArguments"`
	}
	if err := json.Unmarshal(raw, &argument); err != nil {
		return nil, err
	}
	if argument.StartLine == 0 || argument.StartOffset == 0 || argument.EndLine == 0 || argument.EndOffset == 0 {
		return nil, fmt.Errorf("invalid refactor command range")
	}
	uri := protocol.DocumentURI((&url.URL{Scheme: "file", Path: argument.File}).String())
	name, err := d.session.DocumentName(ctx, uri)
	if err != nil {
		return nil, err
	}
	span := protocol.Range{Start: protocol.Position{Line: argument.StartLine - 1, Character: argument.StartOffset - 1}, End: protocol.Position{Line: argument.EndLine - 1, Character: argument.EndOffset - 1}}
	var args *lsp.RefactorArguments
	if argument.InteractiveArguments != nil {
		uri := protocol.DocumentURI((&url.URL{Scheme: "file", Path: argument.InteractiveArguments.TargetFile}).String())
		target, err := d.session.DocumentName(ctx, uri)
		if err != nil {
			return nil, err
		}
		args = &lsp.RefactorArguments{TargetFile: target}
	}
	result, err := d.session.RefactorEdits(ctx, name, span, argument.Refactor, argument.Action, args)
	if err != nil {
		return nil, err
	}
	if result.NotApplicableReason != "" {
		return nil, &lsp.ResponseError{Code: -32603, Message: "Request workspace/executeCommand failed with message: " + result.NotApplicableReason}
	}
	if result.Edits != nil {
		var decision protocol.ApplyWorkspaceEditResult
		decision, err = d.session.ApplyEdits(ctx, *result.Edits, "", lsp.ClientHooks{Capabilities: lsp.ClientCapabilities{ApplyEdit: true, DocumentChanges: true, ResourceOperations: []protocol.ResourceOperationKind{protocol.ResourceCreate}}, ApplyEdit: func(ctx context.Context, params protocol.ApplyWorkspaceEditParams) (protocol.ApplyWorkspaceEditResult, error) {
			legacy := protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{}}
			for _, change := range params.Edit.DocumentChanges {
				if change.TextDocumentEdit != nil {
					document := change.TextDocumentEdit
					legacy.Changes[document.TextDocument.URI] = append(legacy.Changes[document.TextDocument.URI], document.Edits...)
				} else if change.CreateFile == nil {
					return protocol.ApplyWorkspaceEditResult{}, fmt.Errorf("unexpected refactor resource")
				}
			}
			params.Edit = legacy
			raw, err := json.Marshal(params)
			if err != nil {
				return protocol.ApplyWorkspaceEditResult{}, err
			}
			d.clientMu.Lock()
			d.clientRequests = append(d.clientRequests, lspfixture.ClientRequest{Method: "workspace/applyEdit", Params: raw})
			d.clientMu.Unlock()
			if response, ok := d.clientResponses["workspace/applyEdit"]; ok {
				var value protocol.ApplyWorkspaceEditResult
				if err := json.Unmarshal(response, &value); err != nil {
					return value, err
				}
				return value, ctx.Err()
			}
			reason := "capture does not apply client edits"
			return protocol.ApplyWorkspaceEditResult{Applied: false, FailureReason: &reason}, ctx.Err()
		}})
		if err != nil {
			return nil, err
		}
		if decision.Applied && result.Rename != nil {
			params := protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: result.Rename.Position}
			raw, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			d.clientMu.Lock()
			d.clientRequests = append(d.clientRequests, lspfixture.ClientRequest{Method: "_typescript.rename", Params: raw})
			d.clientMu.Unlock()
		}
	}
	return json.RawMessage("null"), nil
}
