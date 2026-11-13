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

package fake

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TestEnvironments is a ports.TestEnvironments in memory. A lease is Ready
// at once, at http://<id>.<namespace>:7070, unless Hold is set; then it stays
// Pending until the test calls Ready. It records each request, so a test
// can read a lease's token and deadline.
type TestEnvironments struct {
	// Hold keeps each new lease Pending.
	Hold bool

	mu       sync.Mutex
	leases   map[envKey]map[string]*fakeLease
	returned []string
	// accounts are the AWS accounts by access key that CheckCredentials
	// knows; nil takes any credential.
	accounts map[string]string
	released []string
	// runnerMissing is what RunnerMissing says (testenv_ci.go).
	runnerMissing string
}

var (
	_ ports.TestEnvironments           = (*TestEnvironments)(nil)
	_ ports.TestEnvironmentCredentials = (*TestEnvironments)(nil)
	_ ports.TestEnvironmentReleaser    = (*TestEnvironments)(nil)
)

// AddAccount makes CheckCredentials take accessKeyID as a key of AWS account
// account; once one is added, it refuses every key it wasn't given.
func (f *TestEnvironments) AddAccount(accessKeyID, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts == nil {
		f.accounts = map[string]string{}
	}
	f.accounts[accessKeyID] = account
}

// CheckCredentials refuses a key it doesn't know, and one of an account
// other than the environment names, as AWS and the aws member would.
func (f *TestEnvironments) CheckCredentials(_ context.Context, env ports.Environment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts == nil {
		return nil
	}
	account, ok := f.accounts[string(env.Secret["accessKeyId"])]
	if !ok {
		return &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: errors.New("InvalidClientTokenId: the security token included in the request is invalid")}
	}
	if env.AWS != nil && env.AWS.Account != "" && env.AWS.Account != account {
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: fmt.Errorf(
			"the credential is of AWS account %s, the environment names account %s", account, env.AWS.Account)}
	}
	return nil
}

// Release records that env was released.
func (f *TestEnvironments) Release(_ context.Context, env ports.Environment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, env.Namespace+"/"+env.Name)
	return nil
}

// Released lists the environments released, as namespace/name, in order.
func (f *TestEnvironments) Released() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.released)
}

type envKey struct{ namespace, name string }

type fakeLease struct {
	lease ports.Lease
	req   ports.LeaseRequest
	// runner is the CI runner a LeaseRunner lease runs (testenv_ci.go).
	runner *ports.JobRunner
}

func keyOf(env ports.Environment) envKey { return envKey{env.Namespace, env.Name} }

// Lease takes a place for req, or returns the one taken for its ID.
func (f *TestEnvironments) Lease(_ context.Context, req ports.LeaseRequest) (ports.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leases == nil {
		f.leases = map[envKey]map[string]*fakeLease{}
	}
	k := keyOf(req.Env)
	if f.leases[k] == nil {
		f.leases[k] = map[string]*fakeLease{}
	}
	if l, ok := f.leases[k][req.ID]; ok {
		return l.lease, nil
	}
	l := &fakeLease{req: req, lease: ports.Lease{ID: req.ID, Holder: req.Holder, State: model.LeaseReady,
		Endpoint: endpoint(req.Env, req.ID), Started: time.Now().Truncate(time.Second)}}
	if f.Hold {
		l.lease.State, l.lease.Endpoint, l.lease.Message = model.LeasePending, "", "held by the test"
	}
	f.leases[k][req.ID] = l
	return l.lease, nil
}

func endpoint(env ports.Environment, id string) string {
	return "http://" + id + "." + env.Namespace + ":7070"
}

// Get reads a lease back.
func (f *TestEnvironments) Get(_ context.Context, env ports.Environment, id string) (ports.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.leases[keyOf(env)][id]; ok {
		return l.lease, nil
	}
	return ports.Lease{}, ports.ErrLeaseNotFound
}

// Return ends a lease; one that is gone is no error.
func (f *TestEnvironments) Return(_ context.Context, env ports.Environment, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.leases[keyOf(env)][id]; ok {
		delete(f.leases[keyOf(env)], id)
		f.returned = append(f.returned, env.Namespace+"/"+id)
	}
	return nil
}

// Leases lists the environment's leases, by ID.
func (f *TestEnvironments) Leases(_ context.Context, env ports.Environment) ([]ports.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.Lease, 0, len(f.leases[keyOf(env)]))
	for _, l := range f.leases[keyOf(env)] {
		out = append(out, l.lease)
	}
	slices.SortFunc(out, func(a, b ports.Lease) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Ready makes a held lease Ready.
func (f *TestEnvironments) Ready(env ports.Environment, id string) {
	f.set(env, id, func(l *ports.Lease) {
		l.State, l.Endpoint, l.Message = model.LeaseReady, endpoint(env, id), ""
	})
}

// Fail makes a lease Failed, with message.
func (f *TestEnvironments) Fail(env ports.Environment, id, message string) {
	f.set(env, id, func(l *ports.Lease) { l.State, l.Endpoint, l.Message = model.LeaseFailed, "", message })
}

// SetCost sets what a lease cost so far.
func (f *TestEnvironments) SetCost(env ports.Environment, id string, milliUSD int64) {
	f.set(env, id, func(l *ports.Lease) { l.CostMilliUSD = milliUSD })
}

func (f *TestEnvironments) set(env ports.Environment, id string, change func(*ports.Lease)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.leases[keyOf(env)][id]; ok {
		change(&l.lease)
	}
}

// Request is the request lease id of env was taken with, and whether it is
// held.
func (f *TestEnvironments) Request(env ports.Environment, id string) (ports.LeaseRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.leases[keyOf(env)][id]
	if !ok {
		return ports.LeaseRequest{}, false
	}
	return l.req, true
}

// Held counts the leases held for env.
func (f *TestEnvironments) Held(env ports.Environment) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.leases[keyOf(env)])
}

// Returned lists the leases returned, as namespace/id, in order.
func (f *TestEnvironments) Returned() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.returned)
}
