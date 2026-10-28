package lsp

import (
	"context"
	"fmt"
	"sync"

	"github.com/totoer/goya/lsp/protocol"
)

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func notification(method string, params any) rpcNotification {
	return rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
}

func (c *rpcConnection) startDiagnostics(s *Session) error {
	if !c.publishDiagnosticsEnabled {
		return nil
	}
	ctx, cancel := context.WithCancel(c.ctx)
	w, err := s.WatchDiagnostics(ctx, &DiagnosticOptions{Capabilities: &c.publishDiagnosticCaps})
	if err != nil {
		cancel()
		return err
	}
	c.runDiagnosticPublisher(ctx, cancel, s, w)
	return nil
}

func (c *rpcConnection) stopDiagnostics() {
	if c.diagnosticCancel != nil {
		c.diagnosticCancel()
	}
	c.diagnosticWG.Wait()
	c.progressWG.Wait()
}

func (s *Session) diagnosticEventCurrent(event DiagnosticEvent) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.ctx.Err() != nil {
		return false
	}
	if event.Generation == s.generation {
		return true
	}
	// An undelivered close clear is still needed unless the resource reopened.
	if event.Kind != DiagnosticEventClear || event.Eager {
		return false
	}
	for name := range s.documents {
		if sourceURI(name, s.environment, s.uriMappings) == event.URI {
			return false
		}
	}
	return true
}

func (c *rpcConnection) runDiagnosticPublisher(ctx context.Context, cancel context.CancelFunc, s *Session, w *DiagnosticStream) {
	c.diagnosticCancel = cancel
	c.diagnosticWG.Add(1)
	go func() {
		defer c.diagnosticWG.Done()
		defer w.Close()
		var progress *diagnosticWireProgress
		defer func() {
			if progress != nil {
				progress.end()
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-w.Events():
				if !ok {
					return
				}
				switch event.Kind {
				case DiagnosticEventProgress:
					if !c.workDoneProgress {
						continue
					}
					if progress != nil {
						progress.end()
						progress = nil
					}
					if event.Progress.Kind == "begin" && s.diagnosticEventCurrent(event) {
						progress = c.beginDiagnosticProgress(ctx, event.Generation)
					}
				case DiagnosticEventResult, DiagnosticEventClear:
					if event.Diagnostics == nil {
						continue
					}
					d := event.Diagnostics
					params := protocol.PublishDiagnosticsParams{URI: event.URI, Diagnostics: d.Items}
					if c.publishDiagnosticVersion {
						params.Version = d.Version
					}
					if _, err := c.sendChecked(ctx, notification("textDocument/publishDiagnostics", params), func() bool { return s.diagnosticEventCurrent(event) }); err != nil {
						return
					}
				case DiagnosticEventError:
					c.log("LSP background diagnostics generation %d: %v", event.Generation, event.Error)
				}
			}
		}
	}()
}

// Progress creation runs separately so clients that never acknowledge optional
// progress cannot stall diagnostics. There is at most one creation per active
// cycle; ending a cycle cancels its pending client request.
type diagnosticWireProgress struct {
	mu           sync.Mutex
	connection   *rpcConnection
	ctx          context.Context
	cancel       context.CancelFunc
	token        string
	begun, ended bool
}

func (c *rpcConnection) beginDiagnosticProgress(parent context.Context, generation uint64) *diagnosticWireProgress {
	select {
	case c.progressSlots <- struct{}{}:
	default:
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	p := &diagnosticWireProgress{connection: c, ctx: ctx, cancel: cancel, token: fmt.Sprintf("goya:diagnostics:%d", generation)}
	c.progressWG.Add(1)
	go func() {
		defer c.progressWG.Done()
		defer func() { <-c.progressSlots }()
		if err := c.requestClient(ctx, "window/workDoneProgress/create", protocol.WorkDoneProgressCreateParams{Token: p.token}, nil); err != nil {
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.ended || ctx.Err() != nil {
			return
		}
		p.begun = c.send(ctx, notification("$/progress", protocol.ProgressParams{Token: p.token, Value: protocol.WorkDoneProgressBegin{Kind: "begin", Title: "Checking TypeScript"}})) == nil
	}()
	return p
}

func (p *diagnosticWireProgress) end() {
	p.cancel()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return
	}
	p.ended = true
	if p.begun {
		_ = p.connection.send(p.connection.ctx, notification("$/progress", protocol.ProgressParams{Token: p.token, Value: protocol.WorkDoneProgressEnd{Kind: "end"}}))
	}
}
