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
	"fmt"
	"os"
	"path/filepath"

	"github.com/JairJosafath/theos/internal/model"
)

// maxPlanSummary keeps the planner's account short: the plan itself is on the
// branch, and the operator reads it there.
const maxPlanSummary = 1000

// plan runs the planner on the brief repository and pushes what it wrote in
// plan/, and nothing else, to the round's branch.
func plan(ctx context.Context, job Job, agent Agent, r *repo, prog *runProgress, result Result) Result {
	guide := r.showBase(ctx, job.Base, model.PlannerGuide)
	brief := r.showBase(ctx, job.Base, model.BriefFile)
	for name, data := range map[string]string{model.PlannerGuide: guide, model.BriefFile: brief} {
		if data == "" {
			result.Error = fmt.Sprintf("the brief repository has no %s on %s", name, job.Base)
			return result
		}
	}
	// Earlier rounds' plans stay in the history; this round writes its own,
	// and goes on with what it wrote before a checkpoint.
	if prog.from == nil {
		if err := os.RemoveAll(filepath.Join(r.dir, model.PlanDir)); err != nil {
			result.Error = err.Error()
			return result
		}
	}
	summary, err := prog.session(ctx, agent, r.dir, PlanPrompt(job, guide, brief))
	result.Summary = shorten(summary, maxPlanSummary)
	if err != nil {
		return agentFailed(job, result, err)
	}
	message := fmt.Sprintf("Plan round %d of brief %s\n\nTask %s.", job.Round, job.Brief, job.Task)
	if err := r.commitAll(ctx, message); err != nil {
		result.Error = err.Error()
		return result
	}
	// Only the operator writes the rest of the repository.
	if err := r.keepOnly(ctx, job.Base, job.Branch, model.PlanDir, message); err != nil {
		result.Error = err.Error()
		return result
	}
	if _, err := r.git(ctx, r.dir, "cat-file", "-e", "HEAD:"+model.PlanDir); err != nil {
		result.Error = "the planner wrote no plan: put it in " + model.PlanDir + "/"
		return result
	}
	if err := r.forcePush(ctx, job.Branch); err != nil {
		result.Error = err.Error()
		return result
	}
	head, err := r.head(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Commit = head
	return result
}
