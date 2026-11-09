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
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// planBriefs records main's brief.md of a Brief's workspace when the Brief
// reconciler makes a Plan Task: what its planner clones, before any later
// reconcile writes the workspace again.
var planBriefs = &planWorkspaces{briefs: map[client.ObjectKey]string{}}

type planWorkspaces struct {
	mu     sync.Mutex
	briefs map[client.ObjectKey]string
	// refused are the namespaces whose Plan Task creates fail, as an API
	// server that refuses them would.
	refused map[string]bool
}

// errPlanCreateFault is the error a refused Plan Task create returns.
var errPlanCreateFault = errors.New("plan Task create refused by the test")

// refuseCreates makes the Plan Task creates in namespace ns fail while
// refuse is set.
func (p *planWorkspaces) refuseCreates(ns string, refuse bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refused == nil {
		p.refused = map[string]bool{}
	}
	p.refused[ns] = refuse
}

func (p *planWorkspaces) refuses(ns string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refused[ns]
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
	task, ok := obj.(*c4v1beta1.Task)
	if !ok || task.Spec.Kind != c4v1beta1.TaskPlan || task.Spec.BriefRef == nil {
		return c.Client.Create(ctx, obj, opts...)
	}
	if c.plans.refuses(task.Namespace) {
		return errPlanCreateFault
	}
	brief := string(gitFake.Repo(task.Namespace, model.BriefRepository(task.Spec.BriefRef.Name)).Files[model.BriefFile].Data)
	err := c.Client.Create(ctx, obj, opts...)
	if err == nil {
		c.plans.mu.Lock()
		c.plans.briefs[client.ObjectKeyFromObject(task)] = brief
		c.plans.mu.Unlock()
	}
	return err
}
