/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// planBriefs records main's brief.md of a Brief's workspace when the Brief
// reconciler makes a Plan Task: what its planner clones, before any later
// reconcile writes the workspace again.
var planBriefs = &planWorkspaces{briefs: map[client.ObjectKey]string{}}

type planWorkspaces struct {
	mu     sync.Mutex
	briefs map[client.ObjectKey]string
}

// briefAt returns main's brief.md when Plan Task key was made.
func (p *planWorkspaces) briefAt(key client.ObjectKey) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	brief, ok := p.briefs[key]
	return brief, ok
}

// wrap is c with its Plan Task creates recorded.
func (p *planWorkspaces) wrap(c client.Client) client.Client {
	return planRecordingClient{Client: c, plans: p}
}

type planRecordingClient struct {
	client.Client
	plans *planWorkspaces
}

func (c planRecordingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	task, ok := obj.(*c4v1alpha1.Task)
	if !ok || task.Spec.Kind != c4v1alpha1.TaskPlan || task.Spec.BriefRef == nil {
		return c.Client.Create(ctx, obj, opts...)
	}
	gitFake.mu.Lock()
	var brief string
	if repo := gitFake.repos[task.Namespace+"/"+model.BriefRepository(task.Spec.BriefRef.Name)]; repo != nil {
		brief = string(repo.files[model.BriefFile].Data)
	}
	gitFake.mu.Unlock()
	err := c.Client.Create(ctx, obj, opts...)
	if err == nil {
		c.plans.mu.Lock()
		c.plans.briefs[client.ObjectKeyFromObject(task)] = brief
		c.plans.mu.Unlock()
	}
	return err
}
