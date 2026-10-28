package lsp

import (
	"context"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestStructureQueriesCapturePositionsVersionsAndOwnTrees(t *testing.T) {
	ctx := context.Background()
	text := "const node = (\n <Panel>\n  text\n </Panel>\n);\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.tsx"}, Files: map[string]string{"/main.tsx": text}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/main.tsx", 3, text); err != nil {
		t.Fatal(err)
	}
	q, err := s.captureQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	positions := []protocol.Position{{Line: 1, Character: 3}, {Line: 3, Character: 4}}
	selection, err := s.prepareStructure(q, "/main.tsx", positions, structureSelection)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.prepareStructure(q, "/main.tsx", positions[:1], structureLinked)
	if err != nil {
		t.Fatal(err)
	}
	folding, err := s.prepareStructure(q, "/main.tsx", nil, structureFolding)
	if err != nil {
		t.Fatal(err)
	}
	positions[0] = protocol.Position{Line: 99}
	if err := s.ChangeDocument(ctx, "/main.tsx", 4, []protocol.ContentChange{{Text: "const node = <Single/>;\n"}}); err != nil {
		t.Fatal(err)
	}
	selected, err := selection(ctx)
	if err != nil || len(selected.selection.Ranges) != 2 || selected.selection.Version == nil || *selected.selection.Version != 3 || selected.selection.Generation != q.snapshot.Generation {
		t.Fatalf("captured selection: %+v %v", selected, err)
	}
	if selected.selection.Ranges[0].Parent == nil {
		t.Fatal("missing parent chain")
	}
	selected.selection.Ranges[0].Parent.Range.Start.Line = 99
	again, err := selection(ctx)
	if err != nil || again.selection.Ranges[0].Parent.Range.Start.Line == 99 {
		t.Fatalf("selection DTO alias: %+v %v", again, err)
	}
	tags, err := linked(ctx)
	if err != nil || tags.linked.Ranges == nil || len(tags.linked.Ranges.Ranges) != 2 || tags.linked.Version == nil || *tags.linked.Version != 3 {
		t.Fatalf("captured linked ranges: %+v %v", tags, err)
	}
	tags.linked.Ranges.Ranges[0].Start.Line = 99
	*tags.linked.Ranges.WordPattern = "tampered"
	next, err := linked(ctx)
	if err != nil || next.linked.Ranges.Ranges[0].Start.Line == 99 || *next.linked.Ranges.WordPattern == "tampered" {
		t.Fatalf("linked DTO alias: %+v %v", next, err)
	}
	folds, err := folding(ctx)
	if err != nil || len(folds.folding.Ranges) == 0 || folds.folding.Version == nil || *folds.folding.Version != 3 {
		t.Fatalf("captured folds: %+v %v", folds, err)
	}
	current, err := s.LinkedEditingRanges(ctx, "/main.tsx", protocol.Position{Character: 16})
	if err != nil || current.Ranges != nil || current.Generation <= q.snapshot.Generation {
		t.Fatalf("self closing tag: %+v %v", current, err)
	}
	empty, err := s.SelectionRanges(ctx, "/main.tsx", nil)
	if err != nil || empty.Ranges == nil || len(empty.Ranges) != 0 {
		t.Fatalf("empty positions: %+v %v", empty, err)
	}
	if _, err := s.SelectionRanges(ctx, "/main.tsx", []protocol.Position{{Line: 99}}); !errors.Is(err, &Error{Code: CodeInvalidDocument}) {
		t.Fatalf("invalid position: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.FoldingRanges(cancelled, "/main.tsx"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.SelectionRanges(cancelled, "/main.tsx", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.LinkedEditingRanges(cancelled, "/main.tsx", protocol.Position{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.FoldingRanges(ctx, "/main.tsx"); err != nil {
		t.Fatal("recovery", err)
	}
}
