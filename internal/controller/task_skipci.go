package controller

import (
	"context"
	"slices"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// pushSkip is what a squash merge that skips main's push run carries: the
// tree of the head whose checks passed, and those checks.
type pushSkip struct {
	headTree string
	checks   []ports.CommitStatus
}

// planSkip decides whether the squash merge of the round's commit skips main's
// push run: its required checks passed and its pull request is not behind main,
// so the merge commit's tree is the head's, which has just passed. It reads the
// head's tree and checks before the merge; nil, so the push run runs, when
// they can't be read.
func (r *TaskReconciler) planSkip(ctx context.Context, provider ports.GitHost, repo ports.Repository,
	status *c4v1beta1.TaskStatus, container *c4v1beta1.Container, behind bool) *pushSkip {
	if behind {
		return nil
	}
	ci, err := containerCI(ctx, r.Client, container)
	if err != nil {
		return nil
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	combined, err := provider.CombinedStatus(gitCtx, repo, status.Commit)
	if err != nil || !model.SkipsPushRun(ci.CheckContexts, combined.State, combined.Contexts, behind) {
		return nil
	}
	statuses, err := provider.CommitStatuses(gitCtx, repo, status.Commit)
	if err != nil {
		return nil
	}
	tree, err := provider.CommitTree(gitCtx, repo, status.Commit)
	if err != nil {
		return nil
	}
	skip := &pushSkip{headTree: tree}
	required := model.RequiredChecks(ci.CheckContexts, combined.Contexts)
	for _, s := range statuses {
		if slices.Contains(required, s.Context) {
			skip.checks = append(skip.checks, s)
		}
	}
	return skip
}

// carryChecks puts the head's required checks onto the merge commit, whose
// push run was skipped, when its tree is the head's: same context, state and
// link. When the trees differ (main moved during the merge) the merge commit
// passed no check, and its required contexts are errors, so main's head does
// not look green. Best effort: a failure is logged, and the next look at main
// finds no status.
func (r *TaskReconciler) carryChecks(ctx context.Context, provider ports.GitHost, repo ports.Repository,
	status *c4v1beta1.TaskStatus, skip *pushSkip) {
	if skip == nil {
		return
	}
	log := logf.FromContext(ctx)
	if status.MergeCommit == "" {
		log.Info("Could not carry the head's checks onto the merge: it has no commit yet", "head", status.Commit)
		return
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	tree, err := provider.CommitTree(gitCtx, repo, status.MergeCommit)
	same := err == nil && tree == skip.headTree
	if err != nil {
		log.Error(err, "Failed to read the merge commit's tree", "commit", status.MergeCommit)
	}
	for _, s := range skip.checks {
		if same {
			s.Description = model.SameTreeDescription(int(status.PullRequest), status.Commit)
		} else {
			s.State, s.Description = ports.CheckError, model.MovedDuringMerge
		}
		if err := provider.SetCommitStatus(gitCtx, repo, status.MergeCommit, s); err != nil {
			log.Error(err, "Failed to write a check onto the merge commit", "commit", status.MergeCommit,
				"context", s.Context)
		}
	}
}
