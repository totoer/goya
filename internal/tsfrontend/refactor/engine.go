// Package refactor implements the pinned editor refactors over borrowed native
// programs. Requests own their trackers; results contain no compiler objects.
package refactor

import (
	"context"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/checker"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/core"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/change"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type Options struct {
	Kind               string
	Invoked            bool
	Implicit           bool
	IncludeInteractive bool
}
type Arguments struct{ TargetFile string }
type EditInfo struct {
	Changes             []ls.ReferenceFileCodeChanges
	RenameFilename      string
	RenamePosition      *int
	NotApplicableReason string
}

type request struct {
	ctx         context.Context
	service     *ls.LanguageService
	program     *compiler.Program
	file        *ast.SourceFile
	span        core.TextRange
	options     Options
	preferences lsutil.UserPreferences
	format      lsutil.FormatCodeSettings
	converters  *lsconv.Converters
	lineMaps    map[string]*lsconv.LSPLineMap
	checker     *checker.Checker
	release     func()
}

func newRequest(ctx context.Context, service *ls.LanguageService, name string, span lsproto.Range, options Options) (*request, error) {
	program := service.GetProgram()
	file := program.GetSourceFile(name)
	if file == nil {
		return nil, ls.ErrNoSourceFile
	}
	maps := map[string]*lsconv.LSPLineMap{}
	converters := lsconv.NewConverters(lsproto.PositionEncodingKindUTF16, func(name string) *lsconv.LSPLineMap {
		if lines := maps[name]; lines != nil {
			return lines
		}
		text := ""
		if source := program.GetSourceFile(name); source != nil {
			text = source.Text()
		}
		lines := lsconv.ComputeLSPLineStarts(text)
		maps[name] = lines
		return lines
	})
	return &request{ctx: ctx, service: service, program: program, file: file, span: converters.FromLSPRange(file, span), options: options, preferences: service.UserPreferences(), format: service.FormatOptions(), converters: converters, lineMaps: maps}, nil
}
func (r *request) close() {
	if r.release != nil {
		r.release()
	}
}
func (r *request) types() *checker.Checker {
	if r.checker == nil {
		r.checker, r.release = r.program.GetTypeCheckerForFile(r.ctx, r.file)
	}
	return r.checker
}
func (r *request) tracker() *change.Tracker {
	return change.NewTracker(r.ctx, r.program.Options(), r.format, r.converters)
}
func finish(tracker *change.Tracker) *EditInfo {
	result := &EditInfo{Changes: []ls.ReferenceFileCodeChanges{}}
	for _, file := range tracker.GetReferenceOrderedChanges() {
		result.Changes = append(result.Changes, ls.ReferenceFileCodeChanges{FileName: file.FileName, TextChanges: file.TextChanges})
	}
	return result
}
func kindBeginsWith(kind, requested string) bool {
	return strings.HasPrefix(kind, requested)
}

type provider struct {
	name      string
	kinds     []string
	available func(*request) []protocol.ApplicableRefactorInfo
	edits     func(*request, string, Arguments) (*EditInfo, error)
}

// Registration order follows the pinned TypeScript namespace imports.
var providers = []provider{bracesProvider, functionProvider, convertExportProvider, convertImportProvider, overloadProvider, destructuredParametersProvider, stringTemplateProvider, optionalChainProvider, extractSymbolProvider, extractTypeProvider, accessorProvider, inferReturnProvider, inlineProvider, moveFileProvider, moveNewFileProvider}

func Applicable(ctx context.Context, service *ls.LanguageService, name string, span lsproto.Range, options Options) ([]protocol.ApplicableRefactorInfo, error) {
	r, err := newRequest(ctx, service, name, span, options)
	if err != nil {
		return nil, err
	}
	defer r.close()
	result := []protocol.ApplicableRefactorInfo{}
	for _, provider := range providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches := false
		for _, kind := range provider.kinds {
			if kindBeginsWith(kind, options.Kind) {
				matches = true
				break
			}
		}
		if matches {
			result = append(result, provider.available(r)...)
		}
	}
	return result, ctx.Err()
}
func Apply(ctx context.Context, service *ls.LanguageService, name string, span lsproto.Range, options Options, refactorName, actionName string, args Arguments) (*EditInfo, error) {
	// TypeScript constructs edit contexts without enumeration trigger/kind filters.
	options = Options{}
	r, err := newRequest(ctx, service, name, span, options)
	if err != nil {
		return nil, err
	}
	defer r.close()
	for _, provider := range providers {
		if provider.name == refactorName {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return provider.edits(r, actionName, args)
		}
	}
	return nil, ctx.Err()
}
