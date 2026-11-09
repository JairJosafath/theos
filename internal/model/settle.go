package model

import "time"

// DefaultScaffoldSettle is how long a new Container waits before its scaffold
// (#43): a model applied in one go creates a Container's Relationships after
// it (kubectl apply -f goes file by file, kustomize doesn't order custom
// resources), and the scaffold reads the Connectors from the Relationships
// there are then, once. The operator's --scaffold-settle sets it.
const DefaultScaffoldSettle = 10 * time.Second

// SettleLeft is how much longer a Container created at created waits, at now,
// before it is scaffolded: zero once settle has passed, and for a settle of
// zero or less.
func SettleLeft(created, now time.Time, settle time.Duration) time.Duration {
	if settle <= 0 {
		return 0
	}
	return max(created.Add(settle).Sub(now), 0)
}
