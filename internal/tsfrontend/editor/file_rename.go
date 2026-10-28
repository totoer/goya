package editor

import (
	"context"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project"
)

// WithFileRenameServices leases all loaded project programs. File operations
// search projects even when the moved file is outside their source graph; the
// global editor preferences apply, as in the pinned server operation.
func (w *Workspace) WithFileRenameServices(ctx context.Context, anchor string, fn func(context.Context, *ls.LanguageService) error) error {
	return w.withServiceSnapshot(ctx, anchor, func(ctx context.Context, _ *ls.LanguageService, _ *project.Snapshot) error {
		var err error
		w.session.WithSnapshotLoadingProjectTree(ctx, nil, func(snapshot *project.Snapshot) {
			for _, p := range snapshot.ProjectCollection.Projects() {
				if err = ctx.Err(); err != nil {
					return
				}
				if p.GetProgram() == nil {
					continue
				}
				service := ls.NewLanguageService(p.Id(), p.GetProgram(), snapshot, "")
				if err = fn(ctx, formattingService(ctx, service)); err != nil {
					return
				}
			}
		})
		if err != nil {
			return err
		}
		return ctx.Err()
	})
}
