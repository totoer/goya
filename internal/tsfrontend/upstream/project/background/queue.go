// Modified for Goya: embedded-language-service.

package background

import (
	"context"
	"sync"
)

// Queue manages background tasks execution
type Queue struct {
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

// NewQueue creates a new background queue for managing background tasks execution.
func NewQueue() *Queue {
	return &Queue{}
}

func (q *Queue) Enqueue(ctx context.Context, fn func(context.Context)) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	// Register work before Close can finish: a concurrent Wait must not race
	// with a new WaitGroup.Go after the closed flag was checked.
	if q.closed || ctx.Err() != nil {
		return
	}

	q.wg.Go(func() {
		// Check context again before executing
		if ctx.Err() != nil {
			return
		}
		fn(ctx)
	})
}

// Wait waits for all active tasks to complete.
// It does not prevent new tasks from being enqueued while waiting.
func (q *Queue) Wait() {
	q.wg.Wait()
}

func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}
