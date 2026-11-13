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
	"errors"
	"fmt"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// A Standard is something an adopted repository grows into (#97,
// docs/plans/adopt-existing.md, decision 7). A Container that adopts its
// repository and names a Standard main doesn't hold is Ready on its identity;
// the operator opens one pull request with the Standard's files that differ,
// and a person merges or closes it: the operator never merges it. Once main
// holds the files the Standard protects as it renders them, the Standard is
// recorded in status.standard and main is protected; until then no agent
// works on the repository. Every outcome is the condition StandardUpdated and
// status.standardAdoption, on a Ready Container.

// awaitsStandard reports whether the Container adopted its repository and
// names a Standard that status doesn't record: main doesn't hold its files
// yet. A Standard named after the adoption arrives the same way.
func awaitsStandard(container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus) bool {
	return container.Spec.Adopt && container.Choices().Standard != "" &&
		status.Phase == c4v1beta1.ContainerReady && status.Standard == nil
}

// standardNotOnMain says why no agent works yet on a Container that adopted
// its repository with a Standard main doesn't hold: main isn't protected by
// the Standard's checks until it does. It is the StandardUpdated condition's
// message, for the Task and the Crew that wait; empty for any other
// Container.
func standardNotOnMain(container *c4v1beta1.Container) string {
	if !awaitsStandard(container, &container.Status) {
		return ""
	}
	cond := apimeta.FindStatusCondition(container.Status.Conditions, c4v1beta1.ConditionStandardUpdated)
	if cond != nil && cond.Status == metav1.ConditionFalse && cond.Message != "" {
		return cond.Message
	}
	return fmt.Sprintf("the files of its Standard %s are not on main yet", container.Choices().Standard)
}

// standardOnMain reports whether the Container's Standard brings main its
// checks: it names one, and isn't still waiting for its files (awaitsStandard).
func standardOnMain(container *c4v1beta1.Container) bool {
	return container.Choices().Standard != "" && !awaitsStandard(container, &container.Status)
}

// adoptionInFlight is the adoption in status as the model sees it, or nil.
func adoptionInFlight(a *c4v1beta1.StandardAdoptionStatus) *model.StandardAdoptionInFlight {
	if a == nil {
		return nil
	}
	return &model.StandardAdoptionInFlight{
		Version:        model.StandardVersion{Ref: a.Ref, Checks: string(a.Checks)},
		PullRequestURL: a.PullRequestURL,
		Stopped:        a.Stopped != "",
		Merged:         a.Stopped == c4v1beta1.ReasonStandardFilesDiffer,
		Base:           a.Base,
	}
}

// adoptionCompared is what one comparison of main with the Standard's render
// read.
type adoptionCompared struct {
	// in is what the Standard rendered with, and rendered its files.
	in       scaffold.Input
	rendered scaffold.Tree
	// head is main's head the files were read at.
	head string
}

// adoptStandard takes the steps this pass can towards main holding the
// Standard's files: it gathers the facts model.NextAdoptionStep asks for and
// does what it decides. It reports true once the Standard is recorded, and
// the pass goes on as for any Container with one.
func (u *standardUpdate) adoptStandard(ctx, gitCtx context.Context, standard *c4v1beta1.Standard) bool {
	log := logf.FromContext(ctx)
	f := model.StandardAdoptionFacts{Name: standard.Name, Target: u.target(standard),
		Adoption: adoptionInFlight(u.status.StandardAdoption)}
	var compared adoptionCompared
	for {
		a := u.status.StandardAdoption
		step := model.NextAdoptionStep(f)
		switch step.Action {
		case model.AdoptionReadPR:
			git, ok := u.access(ctx)
			if !ok {
				return false
			}
			pr, err := git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name), int(a.PullRequest))
			if err != nil {
				u.gitError(ctx, err)
				return false
			}
			f.PR = &model.UpdatePullRequest{Merged: pr.Merged, Closed: pr.State == stateClosed}
		case model.AdoptionReadMain:
			git, ok := u.access(ctx)
			if !ok {
				return false
			}
			_, head, err := git.provider.ReadFiles(gitCtx, git.provider.Repository(u.container.Name),
				ports.DefaultBranch, nil)
			if err != nil {
				// The adoption stopped: the condition keeps saying why.
				log.Info("Could not read main of a stopped Standard adoption", "error", err.Error())
				return false
			}
			f.Main = head
		case model.AdoptionCompare:
			var ok bool
			if compared, f.Diff, ok = u.compareAdoption(ctx, gitCtx, standard, step.Version); !ok {
				return false
			}
			f.Main = compared.head
		case model.AdoptionOpen:
			u.openAdoption(ctx, gitCtx, step.Version, compared, *f.Diff)
			return false
		case model.AdoptionWait:
			u.condition(metav1.ConditionFalse, c4v1beta1.ReasonPullRequestOpen, step.Message)
			return false
		case model.AdoptionRecord:
			return u.recordAdoption(ctx, gitCtx, step.Version, f.Diff.Paths)
		case model.AdoptionStopClosed:
			u.stopAdoption(ctx, gitCtx, c4v1beta1.ReasonUpdateClosed, step.Message, compared.head)
			return false
		case model.AdoptionStopDiffer:
			u.stopAdoption(ctx, gitCtx, c4v1beta1.ReasonStandardFilesDiffer, step.Message, compared.head)
			return false
		case model.AdoptionForget:
			log.Info("Forgot a stopped Standard adoption its Standard or checks moved past",
				"url", a.PullRequestURL, "ref", a.Ref, "stopped", a.Stopped)
			u.status.StandardAdoption = nil
			f.Adoption, f.PR, f.Diff, f.Main = nil, nil, nil, ""
		default: // model.AdoptionNone
			return false
		}
	}
}

// compareAdoption renders the Standard at v, as an adoption renders it, and
// reads main's copies of its files: the paths that differ, and the ones of
// them the Container's protected paths cover. It reports false when it could
// not, with the condition saying why.
func (u *standardUpdate) compareAdoption(ctx, gitCtx context.Context, standard *c4v1beta1.Standard,
	v model.StandardVersion) (adoptionCompared, *model.AdoptionDiff, bool) {
	git, ok := u.access(ctx)
	if !ok {
		return adoptionCompared{}, nil, false
	}
	renders, err := u.renders(ctx, gitCtx, git, standard)
	var rendered scaffold.Tree
	if err == nil {
		renders.in.Checks = v.Checks
		rendered, err = renders.at(gitCtx, v.Ref)
	}
	if err != nil {
		u.renderFailed(ctx, err)
		return adoptionCompared{}, nil, false
	}
	onMain, head, err := git.provider.ReadFiles(gitCtx, git.provider.Repository(u.container.Name),
		ports.DefaultBranch, rendered.Paths())
	if err != nil {
		u.gitError(ctx, err)
		return adoptionCompared{}, nil, false
	}
	protected, err := protectedPaths(ctx, u.r.Client, u.container)
	if err != nil {
		u.gitError(ctx, err)
		return adoptionCompared{}, nil, false
	}
	differ := scaffold.ChangedPaths(rendered, onMain)
	return adoptionCompared{in: renders.in, rendered: rendered, head: head},
		&model.AdoptionDiff{Paths: differ, Protected: model.TouchedPaths(differ, protected)}, true
}

// openAdoption pushes the Standard's files that differ to the adoption's
// branch and opens its pull request. On a Git error the next pass tries again.
func (u *standardUpdate) openAdoption(ctx, gitCtx context.Context, v model.StandardVersion,
	compared adoptionCompared, diff model.AdoptionDiff) {
	git, ok := u.access(ctx)
	if !ok {
		return
	}
	name := compared.in.Standard
	files := scaffold.Tree{}
	for _, p := range diff.Paths {
		files[p] = compared.rendered[p]
	}
	repo := git.provider.Repository(u.container.Name)
	branch := model.StandardAdoptionBranch(name, v)
	author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, compared.head, branch, files, nil,
		scaffold.AdoptionMessage(compared.in, name, v), author)
	if errors.Is(err, ports.ErrEmptyChange) {
		// main got there between the read and the push: the next pass records it.
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	pr, err := git.provider.OpenPullRequest(gitCtx, repo, branch, ports.DefaultBranch,
		model.StandardAdoptionTitle(name, v), model.StandardAdoptionBody(name, v, diff))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	u.status.StandardAdoption = &c4v1beta1.StandardAdoptionStatus{
		Ref: v.Ref, Checks: c4v1beta1.ChecksMode(v.Checks), Branch: branch, Commit: commit, Base: compared.head,
		PullRequest: int32(pr.Number), PullRequestURL: pr.URL, Paths: diff.Paths,
	}
	step := model.NextAdoptionStep(model.StandardAdoptionFacts{Name: name, Target: v,
		Adoption: adoptionInFlight(u.status.StandardAdoption), PR: &model.UpdatePullRequest{}})
	u.condition(metav1.ConditionFalse, c4v1beta1.ReasonPullRequestOpen, step.Message)
	logf.FromContext(ctx).Info("Opened a Standard adoption", "url", pr.URL, "standard", name, "ref", v.Ref,
		"paths", diff.Paths, "protected", diff.Protected)
}

// recordAdoption protects main with the Standard's checks and records the
// Standard at v: main holds the files it protects as it renders them. left
// are its files main holds otherwise, none of them protected: the
// repository's own, which a later update merges with the Standard's changes.
// It reports false on a Git error, and the next pass tries again.
func (u *standardUpdate) recordAdoption(ctx, gitCtx context.Context, v model.StandardVersion, left []string) bool {
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	name := u.container.Choices().Standard
	if err := protectRepository(gitCtx, u.r.Client, git.provider, git.provider.Repository(u.container.Name),
		u.container); err != nil {
		u.gitError(ctx, err)
		return false
	}
	adoption := u.status.StandardAdoption
	u.status.Standard = &c4v1beta1.ContainerStandardStatus{Name: name, Ref: v.Ref, Checks: c4v1beta1.ChecksMode(v.Checks)}
	u.status.StandardAdoption = nil
	url := ""
	if adoption != nil {
		url = adoption.PullRequestURL
	}
	logf.FromContext(ctx).Info("Recorded the Standard of an adopted repository and protected its default branch",
		"standard", name, "ref", v.Ref, "checks", v.Checks, "url", url, "repositoryOwn", left)
	return true
}

// stopAdoption stops the adoption with reason: its pull request ended and
// main is without files the Standard protects. head is main's head they were
// compared at; the adoption is compared again once main moves. The branch of
// a pull request a person closed is deleted.
func (u *standardUpdate) stopAdoption(ctx, gitCtx context.Context, reason, message, head string) {
	log := logf.FromContext(ctx)
	a := u.status.StandardAdoption
	if a.Stopped == "" {
		if reason == c4v1beta1.ReasonUpdateClosed {
			if git, ok := u.access(ctx); ok {
				if err := git.provider.DeleteBranch(gitCtx, git.provider.Repository(u.container.Name), a.Branch); err != nil {
					log.Info("Could not delete the branch of a closed Standard adoption", "branch", a.Branch,
						"error", err.Error())
				}
			}
		}
		log.Info("Stopped a Standard adoption", "url", a.PullRequestURL, "reason", reason, "ref", a.Ref)
	}
	a.Stopped, a.Base = reason, head
	u.condition(metav1.ConditionFalse, reason, message)
}
