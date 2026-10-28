package lsp

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type FoldingRangeResult struct {
	Generation uint64
	Version    *int32
	Ranges     []protocol.FoldingRange
}
type SelectionRangeResult struct {
	Generation uint64
	Version    *int32
	Ranges     []protocol.SelectionRange
}
type LinkedEditingRangeResult struct {
	Generation uint64
	Version    *int32
	Ranges     *protocol.LinkedEditingRanges
}
type structureKind uint8

const (
	structureFolding structureKind = iota
	structureSelection
	structureLinked
)

type structureResult struct {
	folding   FoldingRangeResult
	selection SelectionRangeResult
	linked    LinkedEditingRangeResult
}

// FoldingRanges returns reference line-based folds over the captured document.
func (s *Session) FoldingRanges(ctx context.Context, name string) (FoldingRangeResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return FoldingRangeResult{}, err
	}
	work, err := s.prepareStructure(q, name, nil, structureFolding)
	if err != nil {
		return FoldingRangeResult{}, err
	}
	r, err := work(ctx)
	return r.folding, err
}

// SelectionRanges returns an owned parent chain for each position in order.
func (s *Session) SelectionRanges(ctx context.Context, name string, positions []protocol.Position) (SelectionRangeResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return SelectionRangeResult{}, err
	}
	work, err := s.prepareStructure(q, name, positions, structureSelection)
	if err != nil {
		return SelectionRangeResult{}, err
	}
	r, err := work(ctx)
	return r.selection, err
}

// LinkedEditingRanges returns matching JSX tag ranges, or nil when unavailable.
func (s *Session) LinkedEditingRanges(ctx context.Context, name string, position protocol.Position) (LinkedEditingRangeResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return LinkedEditingRangeResult{}, err
	}
	work, err := s.prepareStructure(q, name, []protocol.Position{position}, structureLinked)
	if err != nil {
		return LinkedEditingRangeResult{}, err
	}
	r, err := work(ctx)
	return r.linked, err
}
func (s *Session) prepareStructure(q *querySnapshot, name string, positions []protocol.Position, kind structureKind) (func(context.Context) (structureResult, error), error) {
	name, _, err := q.position(name, protocol.Position{})
	if err != nil {
		return nil, err
	}
	positions = slices.Clone(positions)
	for i, p := range positions {
		_, positions[i], err = q.position(name, p)
		if err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (structureResult, error) {
		result := structureResult{
			folding:   FoldingRangeResult{Generation: q.snapshot.Generation, Version: q.version(name), Ranges: []protocol.FoldingRange{}},
			selection: SelectionRangeResult{Generation: q.snapshot.Generation, Version: q.version(name), Ranges: []protocol.SelectionRange{}},
			linked:    LinkedEditingRangeResult{Generation: q.snapshot.Generation, Version: q.version(name)},
		}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return result, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			switch kind {
			case structureFolding:
				response, err := service.ProvideFoldingRange(ctx, nativeURI(name))
				if err != nil {
					return err
				}
				text, _ := q.text(name)
				if response.FoldingRanges != nil {
					for _, r := range *response.FoldingRanges {
						item := protocol.FoldingRange{StartLine: r.StartLine, EndLine: r.EndLine}
						if r.Kind != nil {
							item.Kind = protocol.FoldingRangeKind(*r.Kind)
						}
						if item.Kind == protocol.FoldingComment {
							start, err := OffsetAt(text, protocol.Position{Line: r.StartLine})
							if err != nil {
								return err
							}
							line := text[start:]
							if end := strings.IndexAny(line, "\r\n\u2028\u2029"); end >= 0 {
								line = line[:end]
							}
							if hasEndRegionComment(line) {
								continue
							}
						}
						if r.EndCharacter != nil {
							end, err := OffsetAt(text, protocol.Position{Line: r.EndLine, Character: *r.EndCharacter})
							if err != nil {
								return err
							}
							if *r.EndCharacter > 0 && end > 0 && text[end-1] == '}' && item.EndLine > item.StartLine {
								item.EndLine--
							}
						}
						result.folding.Ranges = append(result.folding.Ranges, item)
					}
				}
			case structureSelection:
				nativePositions := make([]lsproto.Position, len(positions))
				for i, p := range positions {
					nativePositions[i] = lsproto.Position{Line: p.Line, Character: p.Character}
				}
				response, err := service.ProvideReferenceSelectionRanges(ctx, &lsproto.SelectionRangeParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Positions: nativePositions})
				if err != nil {
					return err
				}
				var convert func(*lsproto.SelectionRange) *protocol.SelectionRange
				convert = func(r *lsproto.SelectionRange) *protocol.SelectionRange {
					if r == nil {
						return nil
					}
					return &protocol.SelectionRange{Range: publicRange(r.Range), Parent: convert(r.Parent)}
				}
				if response.SelectionRanges != nil {
					for _, r := range *response.SelectionRanges {
						if r != nil {
							result.selection.Ranges = append(result.selection.Ranges, *convert(r))
						}
					}
				}
			case structureLinked:
				p := positions[0]
				response, err := service.ProvideLinkedEditingRange(ctx, &lsproto.LinkedEditingRangeParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: nativeURI(name)}, Position: lsproto.Position{Line: p.Line, Character: p.Character}})
				if err != nil {
					return err
				}
				if r := response.LinkedEditingRanges; r != nil {
					result.linked.Ranges = &protocol.LinkedEditingRanges{Ranges: make([]protocol.Range, 0, len(r.Ranges))}
					for _, span := range r.Ranges {
						result.linked.Ranges.Ranges = append(result.linked.Ranges.Ranges, publicRange(span))
					}
					if r.WordPattern != nil {
						pattern := *r.WordPattern
						result.linked.Ranges.WordPattern = &pattern
					}
				}
			}
			return nil
		})
		if err != nil {
			return structureResult{}, publicServiceError(err)
		}
		return result, lease.ctx.Err()
	}, nil
}

func hasEndRegionComment(line string) bool {
	line = strings.ToLower(line)
	for {
		slash := strings.Index(line, "//")
		if slash < 0 {
			return false
		}
		rest := line[slash+2:]
		trimmed := strings.TrimLeftFunc(rest, func(r rune) bool { return r == '\ufeff' || r != 0x85 && unicode.IsSpace(r) })
		if strings.HasPrefix(trimmed, "#endregion") {
			return true
		}
		line = rest
	}
}
