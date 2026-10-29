package gitprovider

import "github.com/JairJosafath/c4factory/internal/ports"

// The CI port's optional capabilities, aliased while the controllers still
// import this package (#1169).
type (
	OwnerRunners = ports.OwnerRunners
	OwnerActions = ports.OwnerActions
	Runner       = ports.Runner
)
