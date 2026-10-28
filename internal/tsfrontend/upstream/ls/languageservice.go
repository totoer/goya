// Modified for Goya: internal import paths relocated by cmd/importupstream.

// Modified for Goya: embedded-language-service.

package ls

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/compiler"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/autoimport"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsconv"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/sourcemap"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
	"github.com/totoer/goya/internal/tsfrontend/upstream/vfs/vfsmatch"
)

type LanguageService struct {
	projectPath             tspath.Path
	host                    Host
	activeConfig            lsutil.UserPreferences
	program                 *compiler.Program
	converters              *lsconv.Converters
	documentPositionMappers map[string]*sourcemap.DocumentPositionMapper
}

func NewLanguageService(
	projectPath tspath.Path,
	program *compiler.Program,
	host Host,
	activeFile string,
) *LanguageService {
	return &LanguageService{
		projectPath:             projectPath,
		host:                    host,
		program:                 program,
		converters:              host.Converters(),
		activeConfig:            host.GetPreferences(activeFile),
		documentPositionMappers: map[string]*sourcemap.DocumentPositionMapper{},
	}
}

func (l *LanguageService) toPath(fileName string) tspath.Path {
	return tspath.ToPath(fileName, l.program.GetCurrentDirectory(), l.UseCaseSensitiveFileNames())
}

func (l *LanguageService) GetProgram() *compiler.Program {
	return l.program
}

func (l *LanguageService) UserPreferences() lsutil.UserPreferences {
	return l.activeConfig
}

func (l *LanguageService) FormatOptions() lsutil.FormatCodeSettings {
	return l.activeConfig.FormatCodeSettings
}

// WithReferenceFormatCodeSettings gives one request its own editor settings.
// The caller holds the same native snapshot lease for both service values.
func (l *LanguageService) WithReferenceFormatCodeSettings(settings lsutil.FormatCodeSettings) *LanguageService {
	copy := *l
	copy.activeConfig.FormatCodeSettings = settings
	return &copy
}

func (l *LanguageService) tryGetProgramAndFile(fileName string) (*compiler.Program, *ast.SourceFile) {
	program := l.GetProgram()
	file := program.GetSourceFile(fileName)
	return program, file
}

func (l *LanguageService) getProgramAndFile(documentURI lsproto.DocumentUri) (*compiler.Program, *ast.SourceFile) {
	fileName := documentURI.FileName()
	program, file := l.tryGetProgramAndFile(fileName)
	if file == nil {
		panic("file not found: " + fileName)
	}
	return program, file
}

func (l *LanguageService) GetDocumentPositionMapper(fileName string) *sourcemap.DocumentPositionMapper {
	d, ok := l.documentPositionMappers[fileName]
	if !ok {
		d = sourcemap.GetDocumentPositionMapper(l, fileName)
		l.documentPositionMappers[fileName] = d
	}
	return d
}

func (l *LanguageService) ReadFile(fileName string) (string, bool) {
	return l.host.ReadFile(fileName)
}

func (l *LanguageService) UseCaseSensitiveFileNames() bool {
	return l.host.UseCaseSensitiveFileNames()
}

func (l *LanguageService) GetECMALineInfo(fileName string) *sourcemap.ECMALineInfo {
	return l.host.GetECMALineInfo(fileName)
}

// getPreparedAutoImportView returns an auto-import view for the given file if the registry is prepared
// to provide up-to-date auto-imports for it. If not, it returns ErrNeedsAutoImports.
// If auto-imports are disabled via user preferences, it returns (nil, nil).
// ReferenceAutoImportView provides the catalog-only view used by refactors.
func (l *LanguageService) ReferenceAutoImportView(file *ast.SourceFile) (*autoimport.View, error) {
	if !l.host.AutoImportRegistry().IsPreparedForImportingFile(file.FileName(), l.projectPath, l.UserPreferences()) {
		return nil, ErrNeedsAutoImports
	}
	return l.getCurrentAutoImportView(file), nil
}

func (l *LanguageService) getPreparedAutoImportView(fromFile *ast.SourceFile) (*autoimport.View, error) {
	if l.UserPreferences().IncludeCompletionsForModuleExports.IsFalse() {
		return nil, nil
	}
	registry := l.host.AutoImportRegistry()
	if !registry.IsPreparedForImportingFile(fromFile.FileName(), l.projectPath, l.UserPreferences()) {
		return nil, ErrNeedsAutoImports
	}

	view := autoimport.NewView(registry, fromFile, l.projectPath, l.program, l.UserPreferences().ModuleSpecifierPreferences(), l.UserPreferences().IncludePackageJsonAutoImports)
	return view, nil
}

// getCurrentAutoImportView returns an auto-import view for the given file, based on the current state
// of the auto-import registry, which may or may not be up-to-date.
func (l *LanguageService) getCurrentAutoImportView(fromFile *ast.SourceFile) *autoimport.View {
	return autoimport.NewView(
		l.host.AutoImportRegistry(),
		fromFile,
		l.projectPath,
		l.program,
		l.UserPreferences().ModuleSpecifierPreferences(),
		l.UserPreferences().IncludePackageJsonAutoImports,
	)
}

// Used for module specifier completions.
func (l *LanguageService) DirectoryExists(path string) bool {
	return l.host.DirectoryExists(path)
}

// Used for module specifier completions.
func (l *LanguageService) ReadDirectory(path string, extensions []string, includes []string) []string {
	return l.host.ReadDirectory(l.program.GetCurrentDirectory(), path, extensions, nil /*excludes*/, includes, vfsmatch.UnlimitedDepth)
}

func (l *LanguageService) GetDirectories(path string) []string {
	return l.host.GetDirectories(path)
}
