package lsp

import (
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

type wireDiagnosticCapabilities struct {
	RelatedInformation bool `json:"relatedInformation"`
	VersionSupport     bool `json:"versionSupport"`
	TagSupport         *struct {
		ValueSet []protocol.DiagnosticTag `json:"valueSet"`
	} `json:"tagSupport"`
}

func diagnosticClientCapabilities(raw json.RawMessage) (pull, push DiagnosticCapabilities, publish, version bool, err error) {
	// publishDiagnostics has no enable/disable capability. Omitting its options
	// requests the basic notification without optional metadata/version support.
	publish = true
	pull.RelatedInformation = true
	push.RelatedInformation = true
	var wire struct {
		TextDocument *struct {
			Diagnostic         *wireDiagnosticCapabilities `json:"diagnostic"`
			PublishDiagnostics *wireDiagnosticCapabilities `json:"publishDiagnostics"`
		} `json:"textDocument"`
	}
	if err = decodeObject(raw, &wire); err != nil {
		return
	}
	convert := func(w *wireDiagnosticCapabilities) DiagnosticCapabilities {
		caps := DiagnosticCapabilities{RelatedInformation: true}
		if w != nil {
			// TLS includes relatedInformation independently of this optional capability.
			if w.TagSupport != nil {
				caps.TagSupport = append([]protocol.DiagnosticTag{}, w.TagSupport.ValueSet...)
			}
		}
		return caps
	}
	if wire.TextDocument != nil {
		w := wire.TextDocument
		if w.PublishDiagnostics != nil {
			version = w.PublishDiagnostics.VersionSupport
		}
		push = convert(w.PublishDiagnostics)
		pull = convert(w.Diagnostic)
		if w.Diagnostic == nil {
			pull = push
		}
	}
	return
}
