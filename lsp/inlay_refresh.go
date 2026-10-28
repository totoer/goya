package lsp

import (
	"context"
	"encoding/json"
	"reflect"
)

func inlayRefreshCapability(raw json.RawMessage) (bool, error) {
	var caps struct {
		Workspace struct {
			InlayHint struct {
				RefreshSupport bool `json:"refreshSupport"`
			} `json:"inlayHint"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &caps); err != nil {
		return false, &Error{Code: CodeInvalidOptions, Message: "invalid inlay hint refresh capability", Cause: err}
	}
	return caps.Workspace.InlayHint.RefreshSupport, nil
}
func (c *rpcConnection) configure(ctx context.Context, s *Session, configuration WorkspaceConfiguration) error {
	s.mu.RLock()
	previous := cloneInlaySettings(s.editorOptions.InlayHints)
	previousReferences := cloneReferencesCodeLensSettings(s.editorOptions.ReferencesCodeLens)
	previousImplementations := cloneImplementationsCodeLensSettings(s.editorOptions.ImplementationsCodeLens)
	s.mu.RUnlock()
	if err := s.Configure(ctx, configuration); err != nil {
		return err
	}
	s.mu.RLock()
	current := cloneInlaySettings(s.editorOptions.InlayHints)
	currentReferences := cloneReferencesCodeLensSettings(s.editorOptions.ReferencesCodeLens)
	currentImplementations := cloneImplementationsCodeLensSettings(s.editorOptions.ImplementationsCodeLens)
	s.mu.RUnlock()
	if !reflect.DeepEqual(previous, current) {
		c.refreshInlayHints()
	}
	if !reflect.DeepEqual(previousReferences, currentReferences) || !reflect.DeepEqual(previousImplementations, currentImplementations) {
		c.refreshCodeLenses()
	}
	return nil
}

// Refresh requests are coalesced to one pending request and never block the
// protocol reader. Shutdown cancels an unanswered client refresh as well.
func (c *rpcConnection) refreshInlayHints() {
	c.refreshEditorFeature("workspace/inlayHint/refresh", c.inlayRefreshSupport)
}
func (c *rpcConnection) refreshCodeLenses() {
	c.refreshEditorFeature("workspace/codeLens/refresh", c.codeLensRefreshSupport)
}
func (c *rpcConnection) refreshEditorFeature(method string, supported bool) {
	c.mu.Lock()
	if c.closed || c.refreshStopped || !supported || c.refreshPending[method] {
		c.mu.Unlock()
		return
	}
	c.refreshPending[method] = true
	if c.refreshRunning {
		c.mu.Unlock()
		return
	}
	c.refreshRunning = true
	c.refreshWG.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.refreshWG.Done()
		for {
			c.mu.Lock()
			next := ""
			for _, candidate := range []string{"workspace/inlayHint/refresh", "workspace/codeLens/refresh"} {
				if c.refreshPending[candidate] {
					next = candidate
					break
				}
			}
			if next == "" || c.refreshStopped {
				clear(c.refreshPending)
				c.refreshRunning = false
				c.mu.Unlock()
				return
			}
			c.mu.Unlock()
			if err := c.requestClient(c.refreshCtx, next, nil, nil); err != nil && c.refreshCtx.Err() == nil {
				c.log("editor refresh: %v", err)
			}
			c.mu.Lock()
			delete(c.refreshPending, next)
			c.mu.Unlock()
		}
	}()
}
func (c *rpcConnection) stopRefresh() {
	c.mu.Lock()
	c.refreshStopped = true
	c.refreshCancel()
	c.mu.Unlock()
	c.refreshWG.Wait()
}
