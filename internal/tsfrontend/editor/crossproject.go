package editor

import (
	"context"
	"iter"
	"net/url"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/collections"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// WithCrossProjectService keeps the initial snapshot leased while the native
// search loads related projects from the same immutable virtual filesystem.
func (w *Workspace) WithCrossProjectService(ctx context.Context, name string, fn func(context.Context, *ls.LanguageService, ls.CrossProjectOrchestrator) error) error {
	return w.withServiceSnapshot(ctx, name, func(ctx context.Context, service *ls.LanguageService, snapshot *project.Snapshot) error {
		uri := lsproto.DocumentUri((&url.URL{Scheme: "file", Path: name}).String())
		if strings.HasPrefix(name, "bundled:///") {
			uri = lsproto.DocumentUri(name)
		}
		selected := w.projectForSnapshot(snapshot, snapshot.GetDefaultProject(uri), name)
		projects := snapshot.GetProjectsContainingFile(uri)
		found := false
		for _, p := range projects {
			if p.Id() == selected.Id() {
				found = true
				break
			}
		}
		if !found {
			projects = append(projects, selected)
		}
		return fn(ctx, service, &crossProjectSearch{workspace: w, selected: selected, initial: projects})
	})
}

type crossProjectSearch struct {
	workspace *Workspace
	selected  *project.Project
	initial   []ls.Project
}

func (c *crossProjectSearch) GetDefaultProject() ls.Project                 { return c.selected }
func (c *crossProjectSearch) GetAllProjectsForInitialRequest() []ls.Project { return c.initial }
func (c *crossProjectSearch) GetLanguageServiceForProjectWithFile(ctx context.Context, p ls.Project, uri lsproto.DocumentUri) *ls.LanguageService {
	return c.workspace.session.GetLanguageServiceForProjectWithFile(ctx, p.(*project.Project), uri)
}
func (c *crossProjectSearch) GetProjectsForFile(ctx context.Context, uri lsproto.DocumentUri) ([]ls.Project, error) {
	return c.workspace.session.GetProjectsForFile(ctx, uri)
}
func (c *crossProjectSearch) GetProjectsLoadingProjectTree(ctx context.Context, requested *collections.Set[tspath.Path]) iter.Seq[ls.Project] {
	return func(yield func(ls.Project) bool) {
		c.workspace.session.WithSnapshotLoadingProjectTree(ctx, requested, func(snapshot *project.Snapshot) {
			for _, p := range snapshot.ProjectCollection.Projects() {
				if !yield(p) {
					return
				}
			}
		})
	}
}
