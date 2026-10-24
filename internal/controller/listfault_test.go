/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
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
