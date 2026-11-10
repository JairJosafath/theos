/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ports

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// minter is an AgentAccess that mints; checked is one that can also say
// whether it does.
type minter struct{}

func (minter) EnsureAgentAccess(context.Context, Repository, string) (Credentials, error) {
	return Credentials{}, nil
}
func (minter) RemoveAgentAccess(context.Context, Repository, string) error { return nil }
func (minter) RevokeOldAgentTokens(context.Context, string) error         { return nil }

type checked struct {
	minter
	err error
}

func (c checked) CheckAgentAccess() error { return c.err }

// TestAgentAccessState covers #77: what a provider's condition says of its
// agent access. A host that can't say always mints; one that says it can't
// gives its own reason, and any other failure of the check is CheckFailed.
func TestAgentAccessState(t *testing.T) {
	needsApp := &AgentAccessUnavailable{Reason: "NeedsGitHubApp", Message: "The Secret holds no App."}
	for _, tt := range []struct {
		name      string
		access    AgentAccess
		available bool
		reason    string
		message   string
	}{
		{"a host that can't say", minter{}, true, ReasonAgentAccessAvailable,
			"Agents get a credential of their own for each repository."},
		{"a host that says it mints", checked{}, true, ReasonAgentAccessAvailable,
			"Agents get a credential of their own for each repository."},
		{"a host that lacks something", checked{err: needsApp}, false, "NeedsGitHubApp", "The Secret holds no App."},
		{"the same, wrapped", checked{err: fmt.Errorf("checking: %w", needsApp)}, false, "NeedsGitHubApp",
			"The Secret holds no App."},
		{"a check that failed", checked{err: errors.New("no route to host")}, false, ReasonAgentAccessCheckFailed,
			"no route to host"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			available, reason, message := AgentAccessState(tt.access)
			if available != tt.available || reason != tt.reason || message != tt.message {
				t.Errorf("AgentAccessState() = %v, %s, %q; want %v, %s, %q", available, reason, message, tt.available,
					tt.reason, tt.message)
			}
		})
	}
}
