package model

import "time"

// DefaultStaleAfter is how long a Session may go without a heartbeat before the
// operator marks it Stale. The client sends one every 30 seconds.
const DefaultStaleAfter = 2 * time.Minute

// SessionFreshness says whether a Session is stale at now, and if not, how long
// until it would be. A Session that never sent a heartbeat counts from its creation.
func SessionFreshness(lastHeartbeat, created, now time.Time, staleAfter time.Duration) (stale bool, remaining time.Duration) {
	last := lastHeartbeat
	if last.IsZero() {
		last = created
	}
	remaining = last.Add(staleAfter).Sub(now)
	if remaining <= 0 {
		return true, 0
	}
	return false, remaining
}
