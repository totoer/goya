package lsp

import (
	"context"
	"net/url"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type HoverOptions struct {
	SupportsVerbosity bool
	VerbosityLevel    int32
}
type HoverResult struct {
	Generation uint64
	Version    *int32
	Hover      *protocol.Hover
}

// Hover queries the original TS/TSX source without checking/executing a program.
// Nil options use the reference server's Markdown hover without verbosity flags.
func (s *Session) Hover(ctx context.Context, name string, position protocol.Position, options *HoverOptions) (HoverResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return HoverResult{}, err
	}
	work, err := s.prepareHover(q, name, position, options)
	if err != nil {
		return HoverResult{}, err
	}
	return work(ctx)
}
func nativeURI(name string) lsproto.DocumentUri {
	if strings.HasPrefix(name, libraryPrefix) {
		return lsproto.DocumentUri(name)
	}
	return lsproto.DocumentUri((&url.URL{Scheme: "file", Path: name}).String())
}
func publicRange(r lsproto.Range) protocol.Range {
	return protocol.Range{Start: protocol.Position{Line: r.Start.Line, Character: r.Start.Character}, End: protocol.Position{Line: r.End.Line, Character: r.End.Character}}
}

func (s *Session) prepareHover(q *querySnapshot, name string, position protocol.Position, options *HoverOptions) (func(context.Context) (HoverResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	settings := HoverOptions{}
	if options != nil {
		settings = *options
	}
	settings.VerbosityLevel = max(settings.VerbosityLevel, 0)
	return func(ctx context.Context) (HoverResult, error) {
		ctx, formatErr := s.withDocumentFormatting(ctx, q, name)
		if formatErr != nil {
			return HoverResult{}, formatErr
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return HoverResult{}, err
		}
		defer lease.release()
		caps := &lsproto.ResolvedClientCapabilities{}
		caps.TextDocument.Hover.ContentFormat = []lsproto.MarkupKind{lsproto.MarkupKindMarkdown}
		caps.Experimental.HoverVerbosityLevel = settings.SupportsVerbosity
		ctx = lsproto.WithClientCapabilities(lease.ctx, caps)
		result := HoverResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			params := &lsproto.HoverParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Position: lsproto.Position{Line: position.Line, Character: position.Character}}
			if settings.SupportsVerbosity {
				level := settings.VerbosityLevel
				params.VerbosityLevel = &level
			}
			response, err := service.ProvideHover(ctx, params)
			if err != nil {
				return err
			}
			if response.Hover == nil {
				return nil
			}
			hover := response.Hover
			markup := hover.Contents.MarkupContent
			if markup == nil {
				return &Error{Code: CodeInvalidProject, Message: "native hover returned unsupported contents"}
			}
			value := q.mapDocumentationLinks(markup.Value)
			// TLS MarkdownString starts a code block with a newline. Keep this
			// reference presentation at the facade boundary for direct/wire parity.
			if strings.HasPrefix(value, "```") {
				value = "\n" + value
			}
			result.Hover = &protocol.Hover{Contents: protocol.MarkupContent{Kind: protocol.MarkupKind(markup.Kind), Value: value}}
			if hover.Range != nil {
				r := publicRange(*hover.Range)
				result.Hover.Range = &r
			}
			if settings.SupportsVerbosity {
				expand := hover.CanIncreaseVerbosity
				result.Hover.CanIncreaseVerbosityLevel = &expand
			}
			return nil
		})
		if err != nil {
			return HoverResult{}, err
		}
		return result, lease.ctx.Err()
	}, nil
}
