package ports

import (
	"context"
	"errors"
)

// Events is the optional capability of a GitHost that calls the operator back
// when something changes in the owner's repositories. Polling stays the
// contract every host meets; a delivery only shortens the wait. A provider
// has it when it implements this interface (docs/plans/git-events.md).
type Events interface {
	// EnsureEventHook makes the owner's hook deliver to hook.URL, signed
	// with hook.Secret; it creates it, or edits the one whose URL has the
	// same path, removes duplicates, and reports whether it created the
	// hook or changed its URL or secret.
	EnsureEventHook(ctx context.Context, hook EventHook) (changed bool, err error)
	// ParseEvent checks a delivery's signature against secret and says
	// what it concerns. It makes no call to the host. ErrBadSignature
	// when the signature is missing or wrong, or secret is empty.
	ParseEvent(header func(string) string, body, secret []byte) (Event, error)
}

// EventHook is where the owner's hook delivers, and the secret that signs
// each delivery.
type EventHook struct {
	// URL is the receiver's address; its path names the GitProvider.
	URL string
	// Secret signs every delivery.
	Secret string
}

// Event is what a delivery concerns.
type Event struct {
	// Repository is the repository's name under the owner.
	Repository string
	// Number is the issue or pull request, 0 when none.
	Number int
	// Branch is the branch a push or run is on, "" when none or when the
	// host doesn't say (a commit status may name only its commit).
	Branch string
	// Kind is the host's event name, for logs only.
	Kind string
}

// ErrBadSignature is a delivery whose signature is missing or does not match
// the secret.
var ErrBadSignature = errors.New("the delivery's signature is missing or wrong")
