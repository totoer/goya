// Package protocol defines the public editor data types owned by Goya.
// Document positions use zero-based UTF-16 coordinates, as in LSP v3.17.
package protocol

import "encoding/json"

type DocumentURI string

type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int32       `json:"version"`
}

type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int32       `json:"version"`
	Text       string      `json:"text"`
}

// ContentChange replaces the whole document when Range is nil.
type ContentChange struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type TextEdit struct {
	Range        Range  `json:"range"`
	NewText      string `json:"newText"`
	AnnotationID string `json:"annotationId,omitempty"`
}

type DiagnosticSeverity int32

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

type DiagnosticRelatedInformation struct {
	Location Location `json:"location"`
	Message  string   `json:"message"`
}

type DiagnosticTag uint32

const (
	DiagnosticUnnecessary DiagnosticTag = 1
	DiagnosticDeprecated  DiagnosticTag = 2
)

type Diagnostic struct {
	CodeString         *string                        `json:"-"`
	CodeDescription    *DiagnosticCodeDescription     `json:"codeDescription,omitempty"`
	Data               json.RawMessage                `json:"data,omitempty"`
	Range              Range                          `json:"range"`
	Severity           DiagnosticSeverity             `json:"severity,omitempty"`
	Code               int32                          `json:"code,omitempty"`
	Source             string                         `json:"source,omitempty"`
	Message            string                         `json:"message"`
	Tags               []DiagnosticTag                `json:"tags,omitzero"`
	RelatedInformation []DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
}
