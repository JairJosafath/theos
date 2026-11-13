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
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// onboard files the Crew's first issue on a repository its Container adopted
// with no Standard (docs/plans/done/adopt-existing.md, step 5; #98): write
// AGENTS.md and say what you found. Once: status.onboarding records it, and
// an open issue with the marker, which a lost status write leaves, is
// recorded instead of a second one filed. The Crew then picks the issue up
// as any ready one. A failure is logged and tried again at the next poll.
func (r *CrewReconciler) onboard(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
	container *c4v1beta1.Container, provider interface {
		ports.GitHost
		ports.Issues
	}, repo ports.Repository) {
	if !model.FilesOnboarding(model.OnboardingFacts{Adopted: adoptsBare(container),
		Unprotected: unprotected(container), Paused: crew.Spec.Paused, Filed: status.Onboarding != nil}) {
		return
	}
	log := logf.FromContext(ctx)
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	issue, filed, err := fileOnboarding(gitCtx, provider, repo)
	if err != nil {
		log.Error(err, "Failed to file the onboarding issue")
		return
	}
	status.Onboarding = &c4v1beta1.CrewOnboardingStatus{Issue: int32(issue), Filed: metav1.NewTime(r.now())}
	if filed {
		log.Info("Filed the onboarding issue of an adopted repository", "issue", issue)
	}
}

// fileOnboarding files the onboarding issue of repo, unless one is open; it
// returns its number, and whether it filed it now.
func fileOnboarding(ctx context.Context, provider interface {
	ports.GitHost
	ports.Issues
}, repo ports.Repository) (issue int, filed bool, err error) {
	open, err := provider.ListIssues(ctx, repo, model.LabelOnboarding)
	if err != nil {
		return 0, false, fmt.Errorf("could not read the open onboarding issues: %w", err)
	}
	if i := slices.IndexFunc(open, func(i ports.Issue) bool { return model.HasOnboardingMarker(i.Body) }); i >= 0 {
		return open[i].Number, false, nil
	}
	files, _, err := provider.ReadFiles(ctx, repo, ports.DefaultBranch, []string{model.AgentsGuidePath})
	if err != nil {
		return 0, false, fmt.Errorf("could not read %s: %w", model.AgentsGuidePath, err)
	}
	_, hasGuide := files[model.AgentsGuidePath]
	l := model.OnboardingLabel()
	labels := append(issueLabels(),
		ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return 0, false, fmt.Errorf("labels: %w", err)
	}
	release, err := releaseTakingWork(ctx, provider, repo)
	if err != nil {
		return 0, false, err
	}
	title, body := model.OnboardingIssue(hasGuide)
	made, err := provider.CreateIssue(ctx, repo, title, body, []string{model.LabelOnboarding, model.LabelReady}, release)
	if err != nil {
		return 0, false, fmt.Errorf("could not file the issue: %w", err)
	}
	return made.Number, true, nil
}
