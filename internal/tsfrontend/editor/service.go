package editor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/editorregex"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/project"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// WithService holds the native editor snapshot for the complete operation.
// Editor programs may contain syntax/type errors and never become an Analysis.
// The caller serializes operations on this workspace; callbacks are internal
// adapters, never application callbacks.
func (w *Workspace) WithService(ctx context.Context, name string, fn func(context.Context, *ls.LanguageService) error) error {
	return w.withServiceSnapshot(ctx, name, func(ctx context.Context, service *ls.LanguageService, _ *project.Snapshot) error {
		return fn(ctx, service)
	})
}

func (w *Workspace) withServiceSnapshot(ctx context.Context, name string, fn func(context.Context, *ls.LanguageService, *project.Snapshot) error) (resultErr error) {
	defer func() {
		if failure := recover(); failure != nil {
			if budget, ok := failure.(*editorregex.BudgetExceeded); ok {
				resultErr = budget
				if ctx.Err() != nil {
					resultErr = ctx.Err()
				}
			} else if invalid, ok := failure.(*change.InvalidEdits); ok {
				resultErr = invalid
				if ctx.Err() != nil {
					resultErr = ctx.Err()
				}
			} else {
				panic(failure)
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx = lsproto.WithClientCapabilities(ctx, lsproto.GetClientCapabilities(ctx))
	uri := lsproto.DocumentUri((&url.URL{Scheme: "file", Path: name}).String())
	if strings.HasPrefix(name, "bundled:///") {
		uri = lsproto.DocumentUri(name)
	}
	if _, err := w.session.GetProjectsForFile(ctx, uri); err != nil {
		return err
	}
	if err := w.ensureReferencedProject(ctx, name); err != nil {
		return err
	}
	work, err := w.session.WithLanguageServiceAndSnapshot(ctx, uri, func(service *ls.LanguageService, snapshot *project.Snapshot) (func() error, error) {
		var selectedProject tspath.Path
		if w.configFile != "" {
			p := w.projectForSnapshot(snapshot, snapshot.GetDefaultProject(uri), name)
			if p == nil {
				return nil, fmt.Errorf("configured project not found: %s", w.configFile)
			}
			service = ls.NewLanguageService(p.Id(), p.GetProgram(), snapshot, name)
			selectedProject = p.Id()
		}
		if service.GetProgram().GetSourceFile(name) == nil {
			return nil, fmt.Errorf("document is not in selected project: %s", name)
		}
		return func() error {
			err := fn(ctx, formattingService(ctx, service), snapshot)
			if errors.Is(err, ls.ErrNeedsAutoImports) {
				service, err = w.session.GetLanguageServiceWithAutoImports(ctx, snapshot, uri, selectedProject)
				if err != nil {
					return err
				}
				return fn(ctx, formattingService(ctx, service), snapshot)
			}
			return err
		}, nil
	})
	if err != nil {
		return err
	}
	if work != nil {
		err = work()
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}
