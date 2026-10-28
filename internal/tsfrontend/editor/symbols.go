package editor

import (
	"context"

	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project"
)

// WorkspaceSymbols leases the loaded virtual project tree. No compiler state
// escapes the callback and each document is searched only once.
func (w *Workspace) WorkspaceSymbols(ctx context.Context, anchor, query string) ([]*lsproto.SymbolInformation, error) {
	result := []*lsproto.SymbolInformation{}
	collect := func() error {
		var err error
		w.session.WithSnapshotLoadingProjectTree(ctx, nil, func(snapshot *project.Snapshot) {
			var programs []*compiler.Program
			for _, p := range snapshot.ProjectCollection.Projects() {
				if p.GetProgram() != nil {
					programs = append(programs, p.GetProgram())
				}
			}
			result, err = ls.ProvideReferenceWorkspaceSymbols(ctx, programs, snapshot.Converters(), snapshot.UserPreferences(), query)
		})
		if err != nil {
			return err
		}
		return ctx.Err()
	}
	if w.configFile != "" {
		err := collect()
		return result, err
	}
	err := w.withServiceSnapshot(ctx, anchor, func(context.Context, *ls.LanguageService, *project.Snapshot) error { return collect() })
	return result, err
}
