package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/internal/tsfrontend/editor"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
	"github.com/totoer/goya/lsp/protocol"
)

// FormattingOptionsProvider supplies optional document settings without LSP.
// Return nil when unavailable; callbacks must honor cancellation. They execute
// outside session locks and native checker leases.
type FormattingOptionsProvider func(context.Context, protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error)
type formattingProviderKey struct{}
type scopedFormattingProvider struct{ provider FormattingOptionsProvider }

func cloneFormatSettings(value *protocol.FormatCodeSettings) *protocol.FormatCodeSettings {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var result protocol.FormatCodeSettings
	_ = json.Unmarshal(data, &result)
	return &result
}
func cloneFormattingOptions(value *protocol.FormattingOptions) *protocol.FormattingOptions {
	if value == nil {
		return nil
	}
	data, _ := json.Marshal(value)
	var result protocol.FormattingOptions
	_ = json.Unmarshal(data, &result)
	return &result
}
func validateFormatSettings(value *protocol.FormatCodeSettings) error {
	if value == nil {
		return nil
	}
	for _, number := range []*int32{value.BaseIndentSize, value.IndentSize, value.TabSize} {
		if number != nil && *number < 0 {
			return &Error{Code: CodeInvalidOptions, Message: "formatting sizes must be nonnegative"}
		}
		if number != nil && *number > 65536 {
			return &Error{Code: CodeRequestLimit, Message: "formatting size exceeds request budget"}
		}
	}
	if value.TabSize != nil && *value.TabSize == 0 {
		return &Error{Code: CodeInvalidOptions, Message: "formatting tabSize must be positive"}
	}
	if value.IndentStyle != nil && *value.IndentStyle > protocol.IndentSmart {
		return &Error{Code: CodeInvalidOptions, Message: "invalid indentStyle"}
	}
	if value.Semicolons != nil && *value.Semicolons != "" && *value.Semicolons != protocol.SemicolonsIgnore && *value.Semicolons != protocol.SemicolonsInsert && *value.Semicolons != protocol.SemicolonsRemove {
		return &Error{Code: CodeInvalidOptions, Message: "invalid semicolon preference"}
	}
	return nil
}

func (s *Session) formattingSettings(ctx context.Context, q *querySnapshot, name string, request *protocol.FormattingOptions) (lsutil.FormatCodeSettings, error) {
	var zero lsutil.FormatCodeSettings
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := validateFormatSettings(q.snapshot.EditorOptions.Format); err != nil {
		return zero, err
	}
	raw := map[string]any{}
	merge := func(value any) {
		data, _ := json.Marshal(value)
		var fields map[string]any
		_ = json.Unmarshal(data, &fields)
		for key, item := range fields {
			raw[key] = item
		}
	}
	merge(q.snapshot.EditorOptions.Format)
	var tabSize *uint32
	var insertSpaces *bool
	if request != nil {
		if request.TabSize == 0 {
			return zero, &Error{Code: CodeInvalidOptions, Message: "formatting tabSize must be positive"}
		}
		if request.TabSize > 65536 {
			return zero, &Error{Code: CodeRequestLimit, Message: "formatting size exceeds request budget"}
		}
		if err := validateFormatSettings(&request.FormatCodeSettings); err != nil {
			return zero, err
		}
		merge(request)
		tabSize = &request.TabSize
		insertSpaces = &request.InsertSpaces
	} else {
		provider := s.formattingOptionsProvider
		if scoped, ok := ctx.Value(formattingProviderKey{}).(scopedFormattingProvider); ok {
			provider = scoped.provider
		}
		if provider != nil {
			callbackCtx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(s.ctx, cancel)
			options, err := provider(callbackCtx, sourceURI(name, q.snapshot.Environment, q.mappings))
			cancelled := callbackCtx.Err()
			stop()
			cancel()
			if cancelled != nil {
				return zero, cancelled
			}
			if err != nil {
				return zero, err
			}
			if options != nil {
				// Detach the provider's optional pointers before entering native work.
				data, _ := json.Marshal(options)
				var own protocol.DocumentFormattingOptions
				_ = json.Unmarshal(data, &own)
				tabSize, insertSpaces = own.TabSize, own.InsertSpaces
				if tabSize != nil {
					if *tabSize == 0 {
						return zero, &Error{Code: CodeInvalidOptions, Message: "formatting tabSize must be positive"}
					}
					if *tabSize > 65536 {
						return zero, &Error{Code: CodeRequestLimit, Message: "formatting size exceeds request budget"}
					}
					raw["tabSize"] = float64(*tabSize)
				}
				if insertSpaces != nil {
					raw["insertSpaces"] = *insertSpaces
				}
			}
		}
	}
	if _, ok := raw["convertTabsToSpaces"]; !ok && insertSpaces != nil {
		raw["convertTabsToSpaces"] = *insertSpaces
	}
	if _, ok := raw["indentSize"]; !ok && tabSize != nil {
		raw["indentSize"] = float64(*tabSize)
	}
	if _, ok := raw["newLineCharacter"]; !ok {
		raw["newLineCharacter"] = "\n"
	}
	return lsutil.ParseUserPreferences(map[string]any{"typescript": map[string]any{"unstable": raw}}).FormatCodeSettings, nil
}

// withDocumentFormatting resolves optional client settings before acquiring a
// native lease. The editor adapter applies the settings to a request-local LS.
func (s *Session) withDocumentFormatting(ctx context.Context, q *querySnapshot, name string) (context.Context, error) {
	settings, err := s.formattingSettings(ctx, q, name, nil)
	if err != nil {
		return nil, err
	}
	return editor.WithFormatCodeSettings(ctx, settings), nil
}
