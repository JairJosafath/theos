package ports

import "context"

// AgentTokenScopes are what an agent bot's token may do, comma-separated: push
// to its repository, and read the owner's packages, where the go command
// fetches the factory's released modules (Gitea answers a token without it
// with a 401, which stops the go command where a 404 lets it go on). A token
// made with other scopes is out of date.
const AgentTokenScopes = "write:repository,read:package"

// AgentAccess is the agent credential port (docs/architecture.md, "Ports"):
// a credential that writes to one repository and no other, minted for an
// agent's sandboxes from the operator's own, revoked when the agent's work
// is done.
type AgentAccess interface {
	// EnsureAgentAccess makes user a bot that can write to repo only, and returns
	// a fresh token for it.
	EnsureAgentAccess(ctx context.Context, repo Repository, user string) (Credentials, error)
	// RemoveAgentAccess takes away what EnsureAgentAccess gave user on repo: a
	// host where user is a bot account deletes it, and its tokens with it. A
	// user already gone is no error.
	RemoveAgentAccess(ctx context.Context, repo Repository, user string) error
	// RevokeOldAgentTokens revokes the tokens EnsureAgentAccess made for user
	// before an upgrade changed their scopes, and keeps the current one. A user
	// that doesn't exist is no error.
	RevokeOldAgentTokens(ctx context.Context, user string) error
}
