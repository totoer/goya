package lsp

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/tsfrontend"
	"github.com/totoer/goya/lsp/protocol"
)

const libraryPrefix = "bundled:///libs/"

// VirtualDocument is a detached source view. ReadOnly documents belong to the
// frozen host environment or compiler libraries. Version is nil for closed files.
type VirtualDocument struct {
	URI        protocol.DocumentURI `json:"uri"`
	Name       string               `json:"name"`
	Text       string               `json:"text"`
	ReadOnly   bool                 `json:"readOnly"`
	Version    *int32               `json:"version,omitempty"`
	Generation uint64               `json:"generation"`
}

func uriKey(uri protocol.DocumentURI) (string, error) {
	u, err := url.Parse(string(uri))
	if err != nil || u.Scheme == "" || strings.ContainsAny(string(uri), "\x00\r\n") || !utf8.ValidString(string(uri)) {
		return "", &Error{Code: CodeInvalidDocument, Message: "invalid document URI: " + string(uri), Cause: err}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Scheme == "file" && u.Host == "localhost" {
		u.Host = ""
	}
	// Decode only unreserved octets. Encoded path delimiters can identify a
	// different resource in a host scheme and must remain distinct from '/' etc.
	for _, component := range []struct {
		value  string
		target *string
	}{{u.EscapedPath(), &u.RawPath}, {u.RawQuery, &u.RawQuery}, {u.EscapedFragment(), &u.RawFragment}, {u.Opaque, &u.Opaque}} {
		normalized, err := normalizeURIComponent(component.value)
		if err != nil {
			return "", &Error{Code: CodeInvalidDocument, Message: "invalid document URI encoding", Cause: err}
		}
		*component.target = normalized
	}
	return u.String(), nil
}

func normalizeURIComponent(value string) (string, error) {
	const hex = "0123456789ABCDEF"
	digit := func(b byte) int {
		switch {
		case b >= '0' && b <= '9':
			return int(b - '0')
		case b >= 'a' && b <= 'f':
			return int(b-'a') + 10
		case b >= 'A' && b <= 'F':
			return int(b-'A') + 10
		}
		return -1
	}
	unreserved := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b))
	}
	var result strings.Builder
	result.Grow(len(value))
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '%' {
			if i+2 >= len(value) || digit(value[i+1]) < 0 || digit(value[i+2]) < 0 {
				return "", fmt.Errorf("invalid percent escape")
			}
			b = byte(digit(value[i+1])*16 + digit(value[i+2]))
			if unreserved(b) {
				result.WriteByte(b)
			} else {
				result.WriteByte('%')
				result.WriteByte(hex[b>>4])
				result.WriteByte(hex[b&15])
			}
			i += 2
		} else if unreserved(b) || strings.ContainsRune(":/?#[]@!$&'()*+,;=", rune(b)) {
			result.WriteByte(b)
		} else {
			result.WriteByte('%')
			result.WriteByte(hex[b>>4])
			result.WriteByte(hex[b&15])
		}
	}
	return result.String(), nil
}

func captureURIMappings(input map[string]protocol.DocumentURI) (map[string]protocol.DocumentURI, error) {
	result := map[string]protocol.DocumentURI{}
	seen := map[string]string{}
	for name, uri := range input {
		path, err := documentName(name)
		if err != nil {
			return nil, err
		}
		key, err := uriKey(uri)
		if err != nil {
			return nil, err
		}
		parsed, _ := url.Parse(key)
		if parsed.Scheme == "goya-lib" || parsed.Scheme == "goya-host" || parsed.Scheme == "bundled" {
			return nil, &Error{Code: CodeInvalidOptions, Message: "reserved document URI scheme: " + string(uri)}
		}
		if _, ok := result[path]; ok {
			return nil, &Error{Code: CodeInvalidOptions, Message: "duplicate URI mapping for " + path}
		}
		if previous, ok := seen[key]; ok && previous != path {
			return nil, &Error{Code: CodeInvalidOptions, Message: "document URI collision: " + string(uri)}
		}
		result[path] = uri
		seen[key] = path
	}
	return result, nil
}

func sourceURI(name string, environment *goya.Environment, mappings map[string]protocol.DocumentURI) protocol.DocumentURI {
	if rest, ok := strings.CutPrefix(name, libraryPrefix); ok {
		return protocol.DocumentURI((&url.URL{Scheme: "goya-lib", Path: "/" + rest}).String())
	}
	if _, ok := environment.TypeDeclarations()[name]; ok {
		return protocol.DocumentURI((&url.URL{Scheme: "goya-host", Path: name}).String())
	}
	if uri, ok := mappings[name]; ok {
		return uri
	}
	return protocol.DocumentURI((&url.URL{Scheme: "file", Path: name}).String())
}

func validateDocumentURIs(files map[string]string, env *goya.Environment, mappings map[string]protocol.DocumentURI) error {
	seen := map[string]string{}
	add := func(name string) error {
		key, err := uriKey(sourceURI(name, env, mappings))
		if err != nil {
			return err
		}
		if previous, ok := seen[key]; ok && previous != name {
			return &Error{Code: CodeInvalidOptions, Message: "document URI collision: " + key}
		}
		seen[key] = name
		return nil
	}
	for name := range mappings {
		if err := add(name); err != nil {
			return err
		}
	}
	for name := range files {
		if err := add(name); err != nil {
			return err
		}
	}
	for name := range env.TypeDeclarations() {
		if _, ok := files[name]; ok {
			return &Error{Code: CodeInvalidProject, Message: "host declaration collides with project source: " + name}
		}
		if _, ok := mappings[name]; ok {
			return &Error{Code: CodeInvalidOptions, Message: "host declaration URI cannot be replaced: " + name}
		}
	}
	return nil
}

// DocumentURI maps a virtual source name (including a native library target)
// to its client URI. This does not read any document or OS file.
func (s *Session) DocumentURI(ctx context.Context, name string) (protocol.DocumentURI, error) {
	if !strings.HasPrefix(name, libraryPrefix) {
		var err error
		name, err = documentName(name)
		if err != nil {
			return "", err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkLocked(ctx); err != nil {
		return "", err
	}
	return sourceURI(name, s.environment, s.uriMappings), nil
}

// DocumentName resolves a client URI into a virtual name. Unknown custom or
// Windows file URIs require a mapping supplied through SessionOptions.
func (s *Session) DocumentName(ctx context.Context, uri protocol.DocumentURI) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkLocked(ctx); err != nil {
		return "", err
	}
	return s.documentNameLocked(uri)
}

func (s *Session) documentNameLocked(uri protocol.DocumentURI) (string, error) {
	return resolveDocumentURI(uri, s.environment, s.uriMappings)
}

func resolveDocumentURI(uri protocol.DocumentURI, environment *goya.Environment, mappings map[string]protocol.DocumentURI) (string, error) {
	key, err := uriKey(uri)
	if err != nil {
		return "", err
	}
	for name, mapped := range mappings {
		mappedKey, _ := uriKey(mapped)
		if mappedKey == key {
			return name, nil
		}
	}
	u, _ := url.Parse(key)
	if u.Scheme == "goya-lib" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" {
		name := libraryPrefix + strings.TrimPrefix(u.Path, "/")
		if _, ok := tsfrontend.ReadEditorLibrary(name); ok {
			return name, nil
		}
	}
	if u.Scheme == "goya-host" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" {
		if _, ok := environment.TypeDeclarations()[u.Path]; ok {
			return u.Path, nil
		}
	}
	if u.Scheme == "file" && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/") {
		name, err := documentName(u.Path)
		if err != nil {
			return "", err
		}
		canonical, _ := uriKey(sourceURI(name, environment, mappings))
		if canonical == key {
			return name, nil
		}
	}
	return "", &Error{Code: CodeInvalidDocument, Message: "unmapped document URI: " + string(uri)}
}

// ReadDocument returns current text for a source or frozen virtual declaration.
// LSP exposes this operation as goya/readDocument. It never invokes a resolver.
func (s *Session) ReadDocument(ctx context.Context, uri protocol.DocumentURI) (VirtualDocument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkLocked(ctx); err != nil {
		return VirtualDocument{}, err
	}
	name, err := s.documentNameLocked(uri)
	if err != nil {
		return VirtualDocument{}, err
	}
	result := VirtualDocument{URI: sourceURI(name, s.environment, s.uriMappings), Name: name, Generation: s.generation}
	if text, ok := tsfrontend.ReadEditorLibrary(name); ok {
		result.Text = text
		result.ReadOnly = true
		return result, nil
	}
	if text, ok := s.environment.TypeDeclarations()[name]; ok {
		result.Text = text
		result.ReadOnly = true
		return result, nil
	}
	if doc, ok := s.documents[name]; ok {
		result.Text = doc.text
		version := doc.version
		result.Version = &version
		return result, nil
	}
	if text, ok := s.base[name]; ok {
		result.Text = text
		return result, nil
	}
	return VirtualDocument{}, &Error{Code: CodeInvalidDocument, Message: "document not found in virtual catalog: " + name}
}

// UpdateDocumentURIs atomically replaces client mappings, for example after the
// host accepts a file rename. It does not rename sources or access the OS.
func (s *Session) UpdateDocumentURIs(ctx context.Context, mappings map[string]protocol.DocumentURI) error {
	copy, err := captureURIMappings(mappings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	files := maps.Clone(s.base)
	for name, doc := range s.documents {
		files[name] = doc.text
	}
	if err := validateDocumentURIs(files, s.environment, copy); err != nil {
		return err
	}
	s.uriMappings = copy
	s.advanceGenerationLocked()
	return nil
}
