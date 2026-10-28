package lsp

import (
	"context"
	"slices"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type DiagnosticCapabilities struct {
	RelatedInformation bool
	TagSupport         []protocol.DiagnosticTag
}
type DiagnosticOptions struct{ Capabilities *DiagnosticCapabilities }
type DiagnosticResult struct {
	Generation uint64
	URI        protocol.DocumentURI
	Version    *int32
	Items      []protocol.Diagnostic
}

func (s *Session) Diagnostics(ctx context.Context, name string, options *DiagnosticOptions) (DiagnosticResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return DiagnosticResult{}, err
	}
	work, err := s.prepareDiagnostics(q, name, options)
	if err != nil {
		return DiagnosticResult{}, err
	}
	result, err := work(ctx)
	if err == nil {
		s.noteDiagnosticsReady(name, result.Generation)
	}
	return result, err
}
func (s *Session) noteDiagnosticsReady(name string, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation && !s.closed {
		if s.diagnosticsReady == nil {
			s.diagnosticsReady = map[string]uint64{}
		}
		s.diagnosticsReady[name] = generation
	}
}
func diagnosticCapabilities(options *DiagnosticOptions) DiagnosticCapabilities {
	if options == nil || options.Capabilities == nil {
		return DiagnosticCapabilities{RelatedInformation: true, TagSupport: []protocol.DiagnosticTag{protocol.DiagnosticUnnecessary, protocol.DiagnosticDeprecated}}
	}
	value := *options.Capabilities
	value.TagSupport = slices.Clone(value.TagSupport)
	return value
}
func (s *Session) prepareDiagnostics(q *querySnapshot, name string, options *DiagnosticOptions) (func(context.Context) (DiagnosticResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	capabilities := diagnosticCapabilities(options)
	settings := q.snapshot.EditorOptions.Diagnostics
	return func(ctx context.Context) (DiagnosticResult, error) {
		result := DiagnosticResult{Generation: q.snapshot.Generation, URI: sourceURI(name, q.snapshot.Environment, q.mappings), Version: q.version(name), Items: []protocol.Diagnostic{}}
		if settings.DisableValidation {
			if err := s.checkContext(ctx); err != nil {
				return DiagnosticResult{}, err
			}
			return result, nil
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return DiagnosticResult{}, err
		}
		defer lease.release()
		caps := &lsproto.ResolvedClientCapabilities{}
		caps.TextDocument.Diagnostic.RelatedInformation = capabilities.RelatedInformation
		for _, tag := range capabilities.TagSupport {
			caps.TextDocument.Diagnostic.TagSupport.ValueSet = append(caps.TextDocument.Diagnostic.TagSupport.ValueSet, lsproto.DiagnosticTag(tag))
		}
		ctx = lsproto.WithClientCapabilities(lease.ctx, caps)
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvideDiagnostics(ctx, nativeURI(name))
			if err != nil {
				return err
			}
			if response.FullDocumentDiagnosticReport == nil {
				return &Error{Code: CodeInvalidProject, Message: "native diagnostics returned no full report"}
			}
			for _, native := range response.FullDocumentDiagnosticReport.Items {
				if native.Code == nil || native.Code.Integer == nil {
					return &Error{Code: CodeInvalidProject, Message: "native diagnostic has no numeric code"}
				}
				code := *native.Code.Integer
				if slices.Contains(settings.IgnoredCodes, code) {
					continue
				}
				severity := protocol.SeverityError
				if native.Severity != nil {
					severity = protocol.DiagnosticSeverity(*native.Severity)
				}
				if settings.DisableSuggestions && severity == protocol.SeverityHint {
					continue
				}
				message := ""
				if native.Message.String != nil {
					message = *native.Message.String
				} else if native.Message.MarkupContent != nil {
					message = native.Message.MarkupContent.Value
				}
				diagnostic := protocol.Diagnostic{Range: publicRange(native.Range), Severity: severity, Code: code, Source: "typescript", Message: q.moduleDiagnosticMessage(code, message)}
				if capabilities.TagSupport != nil {
					diagnostic.Tags = []protocol.DiagnosticTag{}
					if native.Tags != nil {
						for _, tag := range *native.Tags {
							diagnostic.Tags = append(diagnostic.Tags, protocol.DiagnosticTag(tag))
						}
					}
				}
				if native.RelatedInformation != nil {
					for _, related := range *native.RelatedInformation {
						uri := sourceURI(related.Location.Uri.FileName(), q.snapshot.Environment, q.mappings)
						diagnostic.RelatedInformation = append(diagnostic.RelatedInformation, protocol.DiagnosticRelatedInformation{Location: protocol.Location{URI: uri, Range: publicRange(related.Location.Range)}, Message: related.Message})
					}
				}
				result.Items = append(result.Items, diagnostic)
			}
			return nil
		})
		if err != nil {
			return DiagnosticResult{}, err
		}
		profileDiagnostics, err := lease.state.native.ExecutionProfileDiagnostics(lease.ctx, name)
		if err != nil {
			return DiagnosticResult{}, err
		}
		for _, d := range profileDiagnostics {
			if slices.Contains(settings.IgnoredCodes, d.Code) {
				continue
			}
			var span protocol.Range
			if d.Range != nil {
				span.Start = protocol.Position{Line: uint32(d.Range.Start.Line - 1), Character: uint32(d.Range.Start.Column - 1)}
				span.End = protocol.Position{Line: uint32(d.Range.End.Line - 1), Character: uint32(d.Range.End.Column - 1)}
			}
			result.Items = append(result.Items, protocol.Diagnostic{Range: span, Severity: protocol.SeverityError, Code: d.Code, Source: d.Namespace, Message: d.Message})
		}
		if err := lease.ctx.Err(); err != nil {
			return DiagnosticResult{}, err
		}
		return result, nil
	}, nil
}

// Background diagnostics make source actions available to push clients and
// stream subscribers. Quick fixes keep their separate client-diagnostic state.
func (s *Session) noteSourceDiagnosticsReady(name string, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.generation != generation {
		return
	}
	if s.sourceDiagnosticsReady == nil {
		s.sourceDiagnosticsReady = map[string]uint64{}
	}
	s.sourceDiagnosticsReady[name] = generation
}
