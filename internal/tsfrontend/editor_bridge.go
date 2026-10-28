package tsfrontend

import (
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
	"github.com/totoer/goya/internal/tsfrontend/upstream/vfs"
)

// NewEditorSourceFS shares the compilation frontend's virtual source validation
// and embedded libraries with the editor project system. There is no OS fallback.
func NewEditorSourceFS(files map[string]string) (vfs.FS, error) {
	fs, err := freezeFiles(files)
	if err != nil {
		return nil, err
	}
	return profileFS(fs), nil
}

// EditorDiagnostics retains existing compiler codes and original coordinates.
func EditorDiagnostics(raw []*ast.Diagnostic) []Diagnostic { return convertDiagnostics(raw, nil) }

// ReadEditorLibrary reads only embedded compiler assets, never host OS paths.
func ReadEditorLibrary(name string) (string, bool) {
	if !bundled.IsBundled(name) {
		return "", false
	}
	return profileFS(nil).ReadFile(name)
}
