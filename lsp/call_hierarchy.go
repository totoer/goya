package lsp

import (
	"context"
	"path"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/lsp/protocol"
)

type CallHierarchyPrepareResult struct {
	Generation uint64
	Version    *int32
	Items      []protocol.CallHierarchyItem
}
type IncomingCallResult struct {
	Generation uint64
	Version    *int32
	Calls      []protocol.CallHierarchyIncomingCall
}
type OutgoingCallResult struct {
	Generation uint64
	Version    *int32
	Calls      []protocol.CallHierarchyOutgoingCall
}
type hierarchyTarget struct {
	name     string
	position protocol.Position
}
type hierarchyMetadata struct{ targets []hierarchyTarget }

// PrepareCallHierarchy issues owned items for incoming/outgoing queries. Their
// opaque data is session/generation bound, including on nested call results.
func (s *Session) PrepareCallHierarchy(ctx context.Context, name string, position protocol.Position) (CallHierarchyPrepareResult, error) {
	q, err := s.captureQuery(ctx)
	if err != nil {
		return CallHierarchyPrepareResult{}, err
	}
	work, err := s.prepareCallHierarchy(q, name, position)
	if err != nil {
		return CallHierarchyPrepareResult{}, err
	}
	return work(ctx)
}
func (s *Session) prepareCallHierarchy(q *querySnapshot, name string, position protocol.Position) (func(context.Context) (CallHierarchyPrepareResult, error), error) {
	name, position, err := q.position(name, position)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (CallHierarchyPrepareResult, error) {
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return CallHierarchyPrepareResult{}, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		result := CallHierarchyPrepareResult{Generation: q.snapshot.Generation, Version: q.version(name)}
		var targets []hierarchyTarget
		err = lease.state.native.WithService(ctx, name, func(ctx context.Context, service *ls.LanguageService) error {
			response, err := service.ProvidePrepareCallHierarchy(ctx, nativeURI(name), lsproto.Position{Line: position.Line, Character: position.Character})
			if err != nil {
				return err
			}
			if response.CallHierarchyItems != nil {
				result.Items = make([]protocol.CallHierarchyItem, 0, len(*response.CallHierarchyItems))
				for _, item := range *response.CallHierarchyItems {
					public, target := q.hierarchyItem(item)
					result.Items = append(result.Items, public)
					targets = append(targets, target)
				}
			}
			return nil
		})
		if err != nil {
			return CallHierarchyPrepareResult{}, publicServiceError(err)
		}
		items := make([]*protocol.CallHierarchyItem, len(result.Items))
		for i := range result.Items {
			items[i] = &result.Items[i]
		}
		if err := s.bindHierarchy(ctx, q, items, targets); err != nil {
			return CallHierarchyPrepareResult{}, err
		}
		return result, nil
	}, nil
}

func (q *querySnapshot) hierarchyItem(item *lsproto.CallHierarchyItem) (protocol.CallHierarchyItem, hierarchyTarget) {
	name := item.Uri.FileName()
	selection := publicRange(item.SelectionRange)
	public := protocol.CallHierarchyItem{Name: item.Name, Kind: protocol.SymbolKind(item.Kind), URI: sourceURI(name, q.snapshot.Environment, q.mappings), Range: publicRange(item.Range), SelectionRange: selection}
	if item.Detail != nil {
		public.Detail = *item.Detail
	}
	if item.Tags != nil {
		for _, tag := range *item.Tags {
			public.Tags = append(public.Tags, protocol.SymbolTag(tag))
		}
	}
	if item.Kind == lsproto.SymbolKindFile || item.Kind == lsproto.SymbolKindModule && selection.Start == (protocol.Position{}) {
		public.Name = path.Base(name)
		public.Detail = strings.TrimPrefix(path.Dir(name), "/")
		if public.Detail == "." {
			public.Detail = ""
		}
		if item.Kind == lsproto.SymbolKindFile {
			public.Kind = protocol.SymbolVariable
		}
	}
	return public, hierarchyTarget{name: name, position: selection.Start}
}
func (s *Session) bindHierarchy(ctx context.Context, q *querySnapshot, items []*protocol.CallHierarchyItem, targets []hierarchyTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	if s.generation != q.snapshot.Generation {
		return ErrContentModified
	}
	if len(items) == 0 {
		return nil
	}
	size := 64 + len(targets)*32
	for _, target := range targets {
		size += len(target.name)
	}
	handle := s.retainLocked(hierarchyMetadata{targets: targets}, size)
	for i, item := range items {
		item.Data = &protocol.CallHierarchyData{Handle: string(handle), Index: i}
	}
	return nil
}
func (s *Session) hierarchyQuery(ctx context.Context, item protocol.CallHierarchyItem) (*querySnapshot, hierarchyTarget, error) {
	if item.Data == nil {
		return nil, hierarchyTarget{}, &Error{Code: CodeInvalidHandle, Message: "call hierarchy item has no result handle"}
	}
	data := *item.Data
	s.mu.Lock()
	if err := s.checkLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, hierarchyTarget{}, err
	}
	value, err := s.resolveLocked(ResultHandle(data.Handle))
	if err != nil {
		s.mu.Unlock()
		return nil, hierarchyTarget{}, err
	}
	metadata, ok := value.(hierarchyMetadata)
	if !ok || data.Index < 0 || data.Index >= len(metadata.targets) {
		s.mu.Unlock()
		return nil, hierarchyTarget{}, &Error{Code: CodeInvalidHandle, Message: "invalid call hierarchy item handle or index"}
	}
	target := metadata.targets[data.Index]
	s.mu.Unlock()
	q, err := s.captureQuery(ctx)
	if err != nil {
		return nil, hierarchyTarget{}, err
	}
	s.mu.Lock()
	_, err = s.resolveLocked(ResultHandle(data.Handle))
	s.mu.Unlock()
	if err != nil {
		return nil, hierarchyTarget{}, err
	}
	return q, target, nil
}

// IncomingCalls uses the original issued target; mutable client presentation
// fields and URIs cannot redirect an opaque item to another declaration.
func (s *Session) IncomingCalls(ctx context.Context, item protocol.CallHierarchyItem) (IncomingCallResult, error) {
	work, err := s.prepareHierarchyCalls(ctx, item, true)
	if err != nil {
		return IncomingCallResult{}, err
	}
	r, _, err := work(ctx)
	return r, err
}
func (s *Session) OutgoingCalls(ctx context.Context, item protocol.CallHierarchyItem) (OutgoingCallResult, error) {
	work, err := s.prepareHierarchyCalls(ctx, item, false)
	if err != nil {
		return OutgoingCallResult{}, err
	}
	_, r, err := work(ctx)
	return r, err
}
func (s *Session) prepareHierarchyCalls(ctx context.Context, item protocol.CallHierarchyItem, incoming bool) (func(context.Context) (IncomingCallResult, OutgoingCallResult, error), error) {
	q, target, err := s.hierarchyQuery(ctx, item)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (IncomingCallResult, OutgoingCallResult, error) {
		in := IncomingCallResult{Generation: q.snapshot.Generation, Version: q.version(target.name), Calls: []protocol.CallHierarchyIncomingCall{}}
		out := OutgoingCallResult{Generation: q.snapshot.Generation, Version: q.version(target.name), Calls: []protocol.CallHierarchyOutgoingCall{}}
		lease, err := s.acquireWorkspaceSnapshot(ctx, q)
		if err != nil {
			return in, out, err
		}
		defer lease.release()
		ctx = lsproto.WithClientCapabilities(lease.ctx, &lsproto.ResolvedClientCapabilities{})
		native := &lsproto.CallHierarchyItem{Uri: nativeURI(target.name), SelectionRange: lsproto.Range{Start: lsproto.Position{Line: target.position.Line, Character: target.position.Character}}}
		var targets []hierarchyTarget
		err = lease.state.native.WithService(ctx, target.name, func(ctx context.Context, service *ls.LanguageService) error {
			if incoming {
				response, err := service.ProvideCallHierarchyIncomingCalls(ctx, native, nil)
				if err != nil {
					return err
				}
				if response.CallHierarchyIncomingCalls != nil {
					in.Calls = make([]protocol.CallHierarchyIncomingCall, 0, len(*response.CallHierarchyIncomingCalls))
					for _, call := range *response.CallHierarchyIncomingCalls {
						public, target := q.hierarchyItem(call.From)
						ranges := make([]protocol.Range, 0, len(call.FromRanges))
						for _, r := range call.FromRanges {
							ranges = append(ranges, publicRange(r))
						}
						in.Calls = append(in.Calls, protocol.CallHierarchyIncomingCall{From: public, FromRanges: ranges})
						targets = append(targets, target)
					}
				}
			} else {
				response, err := service.ProvideCallHierarchyOutgoingCalls(ctx, native)
				if err != nil {
					return err
				}
				if response.CallHierarchyOutgoingCalls != nil {
					out.Calls = make([]protocol.CallHierarchyOutgoingCall, 0, len(*response.CallHierarchyOutgoingCalls))
					for _, call := range *response.CallHierarchyOutgoingCalls {
						public, callee := q.hierarchyItem(call.To)
						ranges := make([]protocol.Range, 0, len(call.FromRanges))
						for _, r := range call.FromRanges {
							ranges = append(ranges, publicRange(r))
						}
						out.Calls = append(out.Calls, protocol.CallHierarchyOutgoingCall{To: public, FromRanges: ranges})
						targets = append(targets, callee)
					}
				}
			}
			return nil
		})
		if err != nil {
			return in, out, publicServiceError(err)
		}
		items := make([]*protocol.CallHierarchyItem, len(targets))
		for i := range targets {
			if incoming {
				items[i] = &in.Calls[i].From
			} else {
				items[i] = &out.Calls[i].To
			}
		}
		if err := s.bindHierarchy(ctx, q, items, targets); err != nil {
			return in, out, err
		}
		return in, out, nil
	}, nil
}
