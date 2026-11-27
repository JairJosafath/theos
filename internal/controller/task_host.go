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

package controller

import (
	"context"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/ports"
)

// taskHost is the operator's host for a Task's tick (engine.TaskHost; #336):
// the Container's Git host as the operator's own account (gitRepo), but for
// the round's pull request, which the Container's agent bot opens
// (agentGitRepo). The implementer's pull requests are its work, and the Git
// host refuses a review of one's own pull request, so the operator's account
// could not review them otherwise. The engine's other host calls of the
// steps it carries for the operator (the CI runs read and their failed jobs
// run again) are the operator's account's, as apply's were.
type taskHost struct {
	engine.TaskHost
	// agent is the Container's repository as its agent bot sees it, read
	// when the pull request is opened, as openPR read it.
	agent func(ctx context.Context) (ports.GitHost, ports.Repository, error)
}

var _ engine.TaskHost = (*taskHost)(nil)

// OpenPullRequest opens the pull request from head as the Container's agent
// bot, on the repository of repo's name as the bot sees it; an open one from
// head is returned as it is.
func (h *taskHost) OpenPullRequest(ctx context.Context, repo ports.Repository, head, base, title,
	body string) (ports.PullRequest, error) {
	agent, _, err := h.agent(ctx)
	if err != nil {
		return ports.PullRequest{}, err
	}
	return agent.OpenPullRequest(ctx, agent.Repository(repo.Name), head, base, title, body)
}

// host resolves the Task's host for the engine, the first time a step it
// carries needs it (engine.TaskInput.Host): the operator's account on the
// Container's Git host, and the agent bot's for the pull request.
func (t *taskTurn) host(ctx context.Context) (engine.TaskHost, ports.Repository, error) {
	provider, repo, err := t.r.gitRepo(ctx, t.container)
	if err != nil {
		return nil, ports.Repository{}, err
	}
	return &taskHost{TaskHost: provider, agent: func(ctx context.Context) (ports.GitHost, ports.Repository, error) {
		return t.r.agentGitRepo(ctx, t.container)
	}}, repo, nil
}
