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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// taskStatusFaults fails the Task reconciler's next status writes of chosen
// Tasks, as an API server that refuses them would.
var taskStatusFaults = &statusFaults{left: map[client.ObjectKey]int{}, when: map[client.ObjectKey]func(*c4v1alpha1.Task) bool{}}

// errStatusFault is the error a refused status write returns.
var errStatusFault = errors.New("status write refused by the test")

type statusFaults struct {
	mu   sync.Mutex
	left map[client.ObjectKey]int
	when map[client.ObjectKey]func(*c4v1alpha1.Task) bool
}

// failNext makes the next n status writes of the Task key that would write a
// status when matches fail.
func (f *statusFaults) failNext(key client.ObjectKey, n int, when func(*c4v1alpha1.Task) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left[key], f.when[key] = n, when
}

// pending is how many of the Task key's writes are still to fail.
func (f *statusFaults) pending(key client.ObjectKey) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.left[key]
}

func (f *statusFaults) take(obj client.Object) bool {
	task, ok := obj.(*c4v1alpha1.Task)
	if !ok {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := client.ObjectKeyFromObject(obj)
	if f.left[key] == 0 || !f.when[key](task) {
		return false
	}
	f.left[key]--
	return true
}

// wrap is c with its status writes subject to the faults.
func (f *statusFaults) wrap(c client.Client) client.Client {
	return faultyClient{Client: c, faults: f}
}

type faultyClient struct {
	client.Client
	faults *statusFaults
}

func (c faultyClient) Status() client.SubResourceWriter {
	return faultyStatus{SubResourceWriter: c.Client.Status(), faults: c.faults}
}

type faultyStatus struct {
	client.SubResourceWriter
	faults *statusFaults
}

func (s faultyStatus) Patch(ctx context.Context, obj client.Object, patch client.Patch,
	opts ...client.SubResourcePatchOption) error {
	if s.faults.take(obj) {
		return errStatusFault
	}
	return s.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func (s faultyStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if s.faults.take(obj) {
		return errStatusFault
	}
	return s.SubResourceWriter.Update(ctx, obj, opts...)
}
