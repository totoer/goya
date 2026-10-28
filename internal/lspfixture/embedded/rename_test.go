package embedded_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

// Each edit is applied independently to the exact captured source generation.
// The oracle transcript also compares every edit and its order via both facades.
func TestRecordedRenameEditsCheckInDetachedCatalog(t *testing.T) {
	for _, name := range []string{"rename-alias-shorthand", "rename-overlay", "rename-unicode-crlf", "rename-private-label", "rename-tsx", "rename-projects-overlay"} {
		t.Run(name, func(t *testing.T) {
			file, err := os.Open("../../../testdata/lsp/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := lspfixture.Load(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			options, err := embedded.OptionsForFixture(fixture, "file:///rename")
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			endpoint, err := embedded.NewDirect(ctx, options, fixture.Initialization)
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			checked := 0
			for _, step := range fixture.Steps {
				// These fixtures contain only ordinary document-URI placeholders.
				params := json.RawMessage(strings.ReplaceAll(string(step.Params), "{{ROOT_URI}}", "file:///rename"))
				if step.Notification {
					if err := endpoint.Notify(ctx, step.Method, params); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if step.Method != "textDocument/rename" {
					continue
				}
				raw, err := endpoint.Call(ctx, step.Method, params)
				if err != nil {
					t.Fatal(err)
				}
				var edit *protocol.WorkspaceEdit
				if err := json.Unmarshal(raw, &edit); err != nil {
					t.Fatal(err)
				}
				if edit == nil {
					continue
				}
				snapshot, err := endpoint.Session().Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				copy := snapshot.Project
				copy.Files = maps.Clone(copy.Files)
				for uri, edits := range edit.Changes {
					path, err := endpoint.Session().DocumentName(ctx, uri)
					if err != nil {
						t.Fatal(err)
					}
					copy.Files[path] = applyRenameTextEdits(t, copy.Files[path], edits)
				}
				// A solution fixture is checked from each configured project; its host
				// explicitly selected app config includes redirects to all dependencies.
				diagnostics, err := goya.Check(ctx, copy, snapshot.Environment, snapshot.CompilerOptions)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range diagnostics {
					if d.Severity == "error" {
						t.Fatalf("applied rename did not Check: %+v", diagnostics)
					}
				}
				checked++
				after, err := endpoint.Session().Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !maps.Equal(after.Project.Files, snapshot.Project.Files) {
					t.Fatal("checking proposal mutated session")
				}
			}
			if checked == 0 {
				t.Fatal("no rename proposals checked")
			}
		})
	}
}

func applyRenameTextEdits(t *testing.T, text string, edits []protocol.TextEdit) string {
	t.Helper()
	type replacement struct {
		start, end int
		text       string
	}
	spans := make([]replacement, 0, len(edits))
	for _, edit := range edits {
		start, err := lsp.OffsetAt(text, edit.Range.Start)
		if err != nil {
			t.Fatal(err)
		}
		end, err := lsp.OffsetAt(text, edit.Range.End)
		if err != nil {
			t.Fatal(err)
		}
		if end < start {
			t.Fatal("reversed edit")
		}
		spans = append(spans, replacement{start, end, edit.NewText})
	}
	slices.SortFunc(spans, func(a, b replacement) int { return b.start - a.start })
	last := len(text)
	for _, span := range spans {
		if span.end > last {
			t.Fatal(fmt.Sprintf("overlapping edits: %+v", spans))
		}
		text = text[:span.start] + span.text + text[span.end:]
		last = span.start
	}
	return text
}
