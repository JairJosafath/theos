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
		return agentFailed(result, err)
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
		return agentFailed(result, err)
	}
	verdicts, err := model.ParseScore(output, job.Criteria)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Score = verdicts
	return result
}
