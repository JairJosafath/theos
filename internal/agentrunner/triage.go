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

package agentrunner

import (
	"context"

	"github.com/JairJosafath/theos/internal/model"
)

// triage runs the triager on the base branch and reports its decision. It
// commits and pushes nothing: the operator acts on the decision.
func triage(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, prog *runProgress,
	result Result) Result {
	output, err := prog.session(ctx, agent, r.dir, TriagePrompt(job, ws))
	if err != nil {
		return agentFailed(job, result, err)
	}
	decision, err := ParseTriage(output)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Triage = &decision
	return result
}

// score is a judge run: the judge reads the finished change and the evidence,
// changes nothing, and gives a verdict on each criterion (model.ParseScore).
func score(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, prog *runProgress,
	result Result) Result {
	output, err := prog.session(ctx, agent, r.dir, ScorePrompt(job, ws))
	if err != nil {
		return agentFailed(job, result, err)
	}
	verdicts, err := model.ParseScore(output, job.Criteria)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Score = verdicts
	return result
}
