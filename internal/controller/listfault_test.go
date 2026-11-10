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
)

// crewListFaults fails the Container reconciler's Crew lists in chosen
// namespaces, as an API server that refuses them would.
var crewListFaults = &listFaults{left: map[string]int{}}

// errListFault is the error a refused list returns.
var errListFault = errors.New("list refused by the test")

type listFaults struct {
	mu   sync.Mutex
	left map[string]int
}

// failNext makes the next n Crew lists in namespace ns fail; 0 lets them
// through again.
func (f *listFaults) failNext(ns string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left[ns] = n
}

func (f *listFaults) take(ns string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.left[ns] == 0 {
		return false
	}
	f.left[ns]--
	return true
}

// wrap is c with its Crew lists subject to the faults.
func (f *listFaults) wrap(c client.Client) client.Client {
	return listFaultyClient{Client: c, faults: f}
}

type listFaultyClient struct {
	client.Client
	faults *listFaults
}

func (c listFaultyClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*c4v1beta1.CrewList); ok {
		var lo client.ListOptions
		lo.ApplyOptions(opts)
		if c.faults.take(lo.Namespace) {
			return errListFault
		}
	}
	return c.Client.List(ctx, list, opts...)
}
