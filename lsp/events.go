package lsp

type EventKind string

const EventInvalidated EventKind = "invalidated"

// EventInvalidated means cached editor results must be refreshed. Under pressure
// older invalidations are coalesced; the latest generation remains observable.
type Event struct {
	Kind       EventKind `json:"kind"`
	Generation uint64    `json:"generation"`
}

// Events returns a bounded, nonblocking event stream owned by this session.
// The caller may consume it directly or let the wire facade consume it.
// Close closes the stream. No application callback runs under the state lock.
func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) publishInvalidationLocked() {
	event := Event{Kind: EventInvalidated, Generation: s.generation}
	select {
	case s.events <- event:
		return
	default:
	}
	select {
	case <-s.events:
	default:
	}
	select {
	case s.events <- event:
	default:
	}
}
