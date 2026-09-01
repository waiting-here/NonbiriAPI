package debug

import "fmt"

// ForgetAccount permanently tombstones a deleted account and removes every
// process-local session, trace, event, cursor, and subscriber owned by it.
// Unlike TerminateUser, deletion cleanup emits no terminal event that could
// retain the deleted identity. Repeated calls are safe.
func (hub *Hub) ForgetAccount(userID int64) error {
	if hub == nil || userID <= 0 {
		return ErrInvalid
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.forgotten == nil {
		hub.forgotten = make(map[int64]struct{})
	}
	if _, forgotten := hub.forgotten[userID]; forgotten {
		return nil
	}
	hub.forgotten[userID] = struct{}{}

	for current := range hub.sessions {
		if current != nil && current.userID == userID {
			hub.forgetSessionLocked(current)
		}
	}
	if current := hub.activeByUser[userID]; current != nil {
		hub.forgetSessionLocked(current)
	}
	delete(hub.activeByUser, userID)

	for eventID, known := range hub.knownEvents {
		if known.userID == userID {
			delete(hub.knownEvents, eventID)
		}
	}
	kept := hub.knownOrder[:0]
	for _, eventID := range hub.knownOrder {
		if _, exists := hub.knownEvents[eventID]; exists {
			kept = append(kept, eventID)
		}
	}
	for index := len(kept); index < len(hub.knownOrder); index++ {
		hub.knownOrder[index] = ""
	}
	hub.knownOrder = kept
	return nil
}

func (hub *Hub) forgetSessionLocked(current *session) {
	if current == nil {
		return
	}
	userID := current.userID
	if hub.activeByUser[userID] == current {
		delete(hub.activeByUser, userID)
	}
	current.ended = true
	for _, record := range current.traces {
		if record != nil && record.cancel != nil {
			record.cancel(fmt.Errorf("%w: %s", ErrSessionEnded, EndAccountDeleted))
		}
	}
	for subscriber := range current.subscribers {
		subscriber.closeLocked()
	}
	for _, record := range current.events {
		if record != nil {
			record.inRing = false
			record.pinCount = 0
		}
	}
	for _, record := range current.retainedEvents {
		if record != nil {
			record.inRing = false
			record.pinCount = 0
		}
	}
	delete(hub.sessions, current)
	*current = session{ended: true}
}
