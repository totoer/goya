package lsp

import (
	"context"
	"maps"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/lsp/protocol"
)

// Prepared requests capture state before asynchronous dispatch. They retain only
// immutable source/DTO inputs; native ownership starts at checker admission.
type querySnapshot struct {
	diagnosticsReady       map[string]uint64
	sourceDiagnosticsReady map[string]uint64
	snapshot               Snapshot
	versions               map[string]int32
	mappings               map[string]protocol.DocumentURI
}

func (s *Session) captureQuery(ctx context.Context) (*querySnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkLocked(ctx); err != nil {
		return nil, err
	}
	q := &querySnapshot{diagnosticsReady: maps.Clone(s.diagnosticsReady), sourceDiagnosticsReady: maps.Clone(s.sourceDiagnosticsReady), snapshot: s.snapshotLocked(), versions: make(map[string]int32, len(s.documents)), mappings: maps.Clone(s.uriMappings)}
	for name, doc := range s.documents {
		q.versions[name] = doc.version
	}
	return q, nil
}
func (q *querySnapshot) name(uri protocol.DocumentURI) (string, error) {
	return resolveDocumentURI(uri, q.snapshot.Environment, q.mappings)
}
func (q *querySnapshot) text(name string) (string, bool) {
	if text, ok := q.snapshot.Project.Files[name]; ok {
		return text, true
	}
	if text, ok := q.snapshot.Environment.TypeDeclarations()[name]; ok {
		return text, true
	}
	return tsfrontend.ReadEditorLibrary(name)
}
func (q *querySnapshot) position(name string, p protocol.Position) (string, protocol.Position, error) {
	if !strings.HasPrefix(name, libraryPrefix) {
		var err error
		name, err = documentName(name)
		if err != nil {
			return "", protocol.Position{}, err
		}
	}
	text, ok := q.text(name)
	if !ok {
		return "", protocol.Position{}, &Error{Code: CodeInvalidDocument, Message: "document not found in virtual catalog: " + name}
	}
	offset, err := OffsetAt(text, p)
	if err != nil {
		return "", protocol.Position{}, err
	}
	position, err := PositionAt(text, offset)
	return name, position, err
}
func (q *querySnapshot) version(name string) *int32 {
	if version, ok := q.versions[name]; ok {
		return &version
	}
	return nil
}
