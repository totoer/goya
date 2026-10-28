// Modified for Goya: internal import paths relocated by cmd/importupstream.

package tsoptions

import (
	"sync"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/locale"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

type ParsedBuildCommandLine struct {
	BuildOptions    *core.BuildOptions    `json:"buildOptions"`
	CompilerOptions *core.CompilerOptions `json:"compilerOptions"`
	WatchOptions    *core.WatchOptions    `json:"watchOptions"`
	Projects        []string              `json:"projects"`
	Errors          []*ast.Diagnostic     `json:"errors"`
	Raw             any                   `json:"raw"`

	comparePathsOptions tspath.ComparePathsOptions

	resolvedProjectPaths     []string
	resolvedProjectPathsOnce sync.Once

	locale     locale.Locale
	localeOnce sync.Once
}

func (p *ParsedBuildCommandLine) ResolvedProjectPaths() []string {
	p.resolvedProjectPathsOnce.Do(func() {
		p.resolvedProjectPaths = core.Map(p.Projects, func(project string) string {
			return core.ResolveConfigFileNameOfProjectReference(
				tspath.ResolvePath(p.comparePathsOptions.CurrentDirectory, project),
			)
		})
	})
	return p.resolvedProjectPaths
}

func (p *ParsedBuildCommandLine) Locale() locale.Locale {
	p.localeOnce.Do(func() {
		p.locale, _ = locale.Parse(p.CompilerOptions.Locale)
	})
	return p.locale
}
