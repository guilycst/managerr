package observe

import (
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guilycst/managerr/internal/domain"
)

// Keep directory descriptors bounded. A caller that starts pages and never
// continues them cannot exhaust process descriptors; stale cursors become
// invalid after the idle window and are closed on the next cursor operation.
const (
	maxEnumerationCursors = 256
	enumerationCursorTTL  = 10 * time.Minute
)

type enumerationCursorState struct {
	mu sync.Mutex

	directory *os.File
	stream    *directoryCursor

	rootID         domain.ConfigID
	prefix         string
	directoryID    string
	directoryMTime int64
	sourceID       domain.RuntimeID
	startedAt      time.Time
	priorPartial   bool
	generation     uint64
	observedCount  int64
	invalidated    bool
	// waiters is an internal contention counter. It is useful for deterministic
	// cancellation tests and does not participate in cursor validity.
	waiters       atomic.Int64
	lastUsedNanos atomic.Int64
}

func (state *enumerationCursorState) touch() {
	state.lastUsedNanos.Store(time.Now().UTC().UnixNano())
}

func (state *enumerationCursorState) lastUsed() time.Time {
	return time.Unix(0, state.lastUsedNanos.Load())
}

func (state *enumerationCursorState) closeLocked() {
	if state.directory != nil {
		_ = state.directory.Close()
		state.directory = nil
	}
	state.stream = nil
}

// lockEnumerationCursor returns state with its mutex held. Map and state locks
// are always acquired in this order; callers must release state.mu before
// calling dropEnumerationCursor.
func (o *Observer) lockEnumerationCursor(id string) (*enumerationCursorState, bool) {
	now := time.Now().UTC()
	o.cursorMu.Lock()
	state, ok := o.cursors[id]
	if !ok {
		o.cursorMu.Unlock()
		return nil, false
	}
	state.waiters.Add(1)
	state.mu.Lock()
	state.waiters.Add(-1)
	if now.Sub(state.lastUsed()) > enumerationCursorTTL {
		delete(o.cursors, id)
		state.closeLocked()
		state.mu.Unlock()
		o.cursorMu.Unlock()
		return nil, false
	}
	state.touch()
	o.cursorMu.Unlock()
	return state, true
}

func (o *Observer) storeEnumerationCursor(id string, state *enumerationCursorState) {
	now := time.Now().UTC()
	state.touch()
	o.cursorMu.Lock()
	if o.cursors == nil {
		o.cursors = make(map[string]*enumerationCursorState)
	}
	for key, candidate := range o.cursors {
		if now.Sub(candidate.lastUsed()) <= enumerationCursorTTL {
			continue
		}
		candidate.mu.Lock()
		delete(o.cursors, key)
		candidate.closeLocked()
		candidate.mu.Unlock()
	}
	if len(o.cursors) >= maxEnumerationCursors {
		oldestID := ""
		var oldest time.Time
		for key, candidate := range o.cursors {
			lastUsed := candidate.lastUsed()
			if oldestID == "" || lastUsed.Before(oldest) {
				oldestID, oldest = key, lastUsed
			}
		}
		if oldestID != "" {
			candidate := o.cursors[oldestID]
			candidate.mu.Lock()
			delete(o.cursors, oldestID)
			candidate.closeLocked()
			candidate.mu.Unlock()
		}
	}
	o.cursors[id] = state
	o.cursorMu.Unlock()
}

func (o *Observer) dropEnumerationCursor(id string, state *enumerationCursorState) {
	o.cursorMu.Lock()
	if o.cursors[id] != state {
		o.cursorMu.Unlock()
		return
	}
	state.mu.Lock()
	state.invalidated = true
	delete(o.cursors, id)
	state.closeLocked()
	state.mu.Unlock()
	o.cursorMu.Unlock()
}

// invalidateEnumerationCursor closes one cursor without requiring a full
// continuation. Generation still must match, so cancellation of an already
// stale token cannot invalidate a newer valid token for the same stream.
func (o *Observer) invalidateEnumerationCursor(id string, generation uint64) {
	o.cursorMu.Lock()
	state, ok := o.cursors[id]
	if !ok {
		o.cursorMu.Unlock()
		return
	}
	state.mu.Lock()
	if state.generation != generation {
		state.mu.Unlock()
		o.cursorMu.Unlock()
		return
	}
	state.invalidated = true
	delete(o.cursors, id)
	state.closeLocked()
	state.mu.Unlock()
	o.cursorMu.Unlock()
}
