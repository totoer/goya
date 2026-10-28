package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type SignatureHelpRequestOptions struct {
	Context *protocol.SignatureHelpContext
}
type SignatureHelpResult struct {
	Generation uint64
	Version    *int32
	Help       *protocol.SignatureHelp
}

// SignatureHelp describes the call at the original UTF-16 source position.
// It never executes a Go binding. Context may retain a client's selected overload
// across retriggers; returned data is detached from the program and input.
func (s *Session) SignatureHelp(ctx context.Context, name string, position protocol.Position, options *SignatureHelpRequestOptions) (SignatureHelpResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return SignatureHelpResult{}, err
	}
	work, err := s.prepareSignatureHelp(q, name, position, options)
	if err != nil {
		return SignatureHelpResult{}, err
	}
	return work(ctx)
}

func (s *Session) prepareSignatureHelp(q *querySnapshot, name string, position protocol.Position, options *SignatureHelpRequestOptions) (func(context.Context) (SignatureHelpResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	var previous *protocol.SignatureHelpContext
	var nativeContext *lsproto.SignatureHelpContext
	if options != nil && options.Context != nil {
		if options.Context.TriggerKind < protocol.SignatureHelpInvoked || options.Context.TriggerKind > protocol.SignatureHelpContentChange {
			return nil, &Error{Code: CodeInvalidOptions, Message: "invalid signature help trigger kind"}
		}
		data, err := json.Marshal(options.Context)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &previous); err != nil {
			return nil, err
		}
		// Prior results influence only overload selection, not the native checker.
		nativeContext = &lsproto.SignatureHelpContext{TriggerKind: lsproto.SignatureHelpTriggerKind(previous.TriggerKind), TriggerCharacter: previous.TriggerCharacter, IsRetrigger: previous.IsRetrigger}
	}
	return func(ctx context.Context) (SignatureHelpResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return SignatureHelpResult{}, err
		}
		defer lease.release()
		caps := &lsproto.ResolvedClientCapabilities{}
		caps.TextDocument.SignatureHelp.SignatureInformation.DocumentationFormat = []lsproto.MarkupKind{lsproto.MarkupKindMarkdown}
		ctx = lsproto.WithClientCapabilities(lease.ctx, caps)
		result := SignatureHelpResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvideSignatureHelp(ctx, nativeURI(name), lsproto.Position{Line: position.Line, Character: position.Character}, nativeContext)
			if err != nil {
				return err
			}
			help := response.SignatureHelp
			if help == nil {
				return nil
			}
			out := &protocol.SignatureHelp{Signatures: make([]protocol.SignatureInformation, len(help.Signatures)), ActiveSignature: new(uint32(0)), ActiveParameter: new(uint32(0))}
			if help.ActiveSignature != nil {
				*out.ActiveSignature = *help.ActiveSignature
			}
			if help.ActiveParameter != nil && help.ActiveParameter.Uinteger != nil {
				*out.ActiveParameter = *help.ActiveParameter.Uinteger
			}
			for i, signature := range help.Signatures {
				target := &out.Signatures[i]
				target.Label = signature.Label
				target.Documentation = q.signatureDocumentation(signature.Documentation)
				target.Parameters = []protocol.ParameterInformation{}
				if signature.Parameters != nil {
					for _, parameter := range *signature.Parameters {
						label := ""
						if parameter.Label.String != nil {
							label = *parameter.Label.String
						}
						target.Parameters = append(target.Parameters, protocol.ParameterInformation{Label: label, Documentation: q.signatureDocumentation(parameter.Documentation)})
					}
				}
			}
			// TLS clamps the argument marker once more after the compiler's
			// middle-rest fallback. Keep its visible last-parameter selection.
			if int(*out.ActiveSignature) < len(out.Signatures) {
				parameters := out.Signatures[*out.ActiveSignature].Parameters
				if len(parameters) > 0 && *out.ActiveParameter >= uint32(len(parameters)) {
					for _, parameter := range parameters {
						if strings.HasPrefix(parameter.Label, "...") {
							*out.ActiveParameter = uint32(len(parameters) - 1)
							break
						}
					}
				}
			}
			if previous != nil && previous.IsRetrigger && previous.ActiveSignatureHelp != nil {
				old := previous.ActiveSignatureHelp
				if old.ActiveSignature != nil && int(*old.ActiveSignature) < len(old.Signatures) {
					for i, sig := range out.Signatures {
						if sig.Label == old.Signatures[*old.ActiveSignature].Label {
							*out.ActiveSignature = uint32(i)
							break
						}
					}
				}
			}
			result.Help = out
			return nil
		})
		if err != nil {
			return SignatureHelpResult{}, err
		}
		return result, lease.ctx.Err()
	}, nil
}
func (q *querySnapshot) signatureDocumentation(doc *lsproto.StringOrMarkupContent) *protocol.StringOrMarkupContent {
	if doc == nil {
		return nil
	}
	if doc.String != nil {
		value := q.mapDocumentationLinks(*doc.String)
		return &protocol.StringOrMarkupContent{String: &value}
	}
	if doc.MarkupContent != nil {
		return &protocol.StringOrMarkupContent{MarkupContent: &protocol.MarkupContent{Kind: protocol.MarkupKind(doc.MarkupContent.Kind), Value: q.mapDocumentationLinks(doc.MarkupContent.Value)}}
	}
	return nil
}

func signatureHelpRequestPreparers(s *Session) map[string]requestPreparer {
	return map[string]requestPreparer{"textDocument/signatureHelp": func(ctx context.Context, _ *rpcConnection, raw json.RawMessage) (requestWork, error) {
		var params struct {
			TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
			Position     *protocol.Position               `json:"position"`
			Context      *protocol.SignatureHelpContext   `json:"context"`
		}
		if err := decodeObject(raw, &params); err != nil {
			return nil, err
		}
		if params.TextDocument == nil || params.Position == nil {
			return nil, &Error{Code: CodeInvalidOptions, Message: "signatureHelp requires textDocument and position"}
		}
		q, err := s.captureQuery(ctx)
		if err != nil {
			return nil, err
		}
		name, err := q.name(params.TextDocument.URI)
		if err != nil {
			return nil, err
		}
		work, err := s.prepareSignatureHelp(q, name, *params.Position, &SignatureHelpRequestOptions{Context: params.Context})
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) { r, err := work(ctx); return r.Help, err }, nil
	}}
}
