package lsp

import (
	"context"
	"encoding/json"
	"github.com/totoer/goya/lsp/protocol"
)

func cloneDocumentFormattingOptions(options *protocol.DocumentFormattingOptions) *protocol.DocumentFormattingOptions {
	if options == nil {
		return nil
	}
	data, _ := json.Marshal(options)
	var result protocol.DocumentFormattingOptions
	_ = json.Unmarshal(data, &result)
	return &result
}
func workspaceConfigurationCapability(raw json.RawMessage) (bool, error) {
	var caps struct {
		Workspace struct {
			Configuration bool `json:"configuration"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		return false, &Error{Code: CodeInvalidOptions, Message: "invalid workspace configuration capability", Cause: err}
	}
	return caps.Workspace.Configuration, nil
}
func (c *rpcConnection) documentFormattingOptions(ctx context.Context, uri protocol.DocumentURI) (*protocol.DocumentFormattingOptions, error) {
	c.mu.Lock()
	cached, ok := c.formattingCache[uri]
	c.mu.Unlock()
	if ok {
		return cloneDocumentFormattingOptions(cached), nil
	}
	var result []json.RawMessage
	if err := c.requestClient(ctx, "workspace/configuration", map[string]any{"items": []any{map[string]any{"scopeUri": uri, "section": "formattingOptions"}}}, &result); err != nil {
		return nil, err
	}
	var options *protocol.DocumentFormattingOptions
	if len(result) > 0 {
		var data map[string]json.RawMessage
		if json.Unmarshal(result[0], &data) == nil && data != nil {
			own := protocol.DocumentFormattingOptions{}
			if raw, ok := data["tabSize"]; ok && string(raw) != "null" {
				var size uint32
				if json.Unmarshal(raw, &size) == nil {
					own.TabSize = &size
				}
			}
			if raw, ok := data["insertSpaces"]; ok && string(raw) != "null" {
				var spaces bool
				if json.Unmarshal(raw, &spaces) == nil {
					own.InsertSpaces = &spaces
				}
			}
			options = &own
		}
	}
	c.mu.Lock()
	if !c.closed {
		if _, exists := c.formattingCache[uri]; !exists {
			const limit = 256
			if len(c.formattingCacheKeys) >= limit {
				delete(c.formattingCache, c.formattingCacheKeys[0])
				c.formattingCacheKeys = c.formattingCacheKeys[1:]
			}
			c.formattingCacheKeys = append(c.formattingCacheKeys, uri)
		}
		c.formattingCache[uri] = cloneDocumentFormattingOptions(options)
	}
	c.mu.Unlock()
	return options, nil
}
func (c *rpcConnection) withFormattingProvider(ctx context.Context) context.Context {
	if !c.workspaceConfiguration {
		return ctx
	}
	return context.WithValue(ctx, formattingProviderKey{}, scopedFormattingProvider{provider: c.documentFormattingOptions})
}
