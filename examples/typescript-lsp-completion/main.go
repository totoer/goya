// This example resolves an auto-import and applies the proposed edits to a
// caller-owned buffer before reporting the new version to the editor session.
package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func main() {
	ctx := context.Background()
	source := "export {};\nappe(41);\n"
	session, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{
		"/tsconfig.json": `{"files":["main.ts","helper.ts"]}`,
		"/main.ts":       source,
		"/helper.ts":     "export function append(value: number): number { return value + 1; }\n",
	}}})
	must(err)
	defer session.Close()
	must(session.OpenDocument(ctx, "/main.ts", 1, source))
	completions, err := session.Completion(ctx, "/main.ts", protocol.Position{Line: 1, Character: 4}, nil)
	must(err)
	var selected *protocol.CompletionItem
	for _, item := range completions.List.Items {
		if item.Label == "append" {
			selected = &item
			break
		}
	}
	if selected == nil {
		panic("append was not suggested")
	}
	resolved, err := session.ResolveCompletion(ctx, *selected)
	must(err)
	fmt.Printf("completion=%s imports=%d\n", resolved.Item.Label, len(resolved.Item.AdditionalTextEdits))
	edit := resolved.Item.TextEdit
	if edit == nil {
		panic("completion has no edit")
	}
	replacement := edit.Range
	if replacement == nil {
		replacement = edit.Replace
	}
	if replacement == nil {
		panic("completion has no replacement range")
	}
	edits := slices.Clone(resolved.Item.AdditionalTextEdits)
	edits = append(edits, protocol.TextEdit{Range: *replacement, NewText: edit.NewText})
	source = apply(source, edits)
	must(session.ChangeDocument(ctx, "/main.ts", 2, []protocol.ContentChange{{Text: source}}))
	// Original UTF-16 coordinates include the new two-line import prefix.
	signature, err := session.SignatureHelp(ctx, "/main.ts", protocol.Position{Line: 3, Character: 7}, nil)
	must(err)
	if signature.Help == nil {
		panic("signature help missing")
	}
	diagnostics, err := session.Diagnostics(ctx, "/main.ts", nil)
	must(err)
	snapshot, err := session.ProjectSnapshot(ctx, "/main.ts")
	must(err)
	program, compilerDiagnostics, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	must(err)
	fmt.Printf("signature=%s diagnostics=%d compiled=%t errors=%t\n", signature.Help.Signatures[0].Label, len(diagnostics.Items), program != nil, goya.HasTypeErrors(compilerDiagnostics))
}

func apply(text string, edits []protocol.TextEdit) string {
	type change struct {
		start, end int
		text       string
	}
	changes := make([]change, 0, len(edits))
	for _, edit := range edits {
		start, err := lsp.OffsetAt(text, edit.Range.Start)
		must(err)
		end, err := lsp.OffsetAt(text, edit.Range.End)
		must(err)
		changes = append(changes, change{start, end, edit.NewText})
	}
	slices.SortFunc(changes, func(a, b change) int {
		if a.start != b.start {
			return b.start - a.start
		}
		return b.end - a.end
	})
	previous := len(text) + 1
	for _, edit := range changes {
		if edit.start > edit.end || edit.end > previous {
			panic("overlapping edits")
		}
		text = text[:edit.start] + edit.text + text[edit.end:]
		previous = edit.start
	}
	return text
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
