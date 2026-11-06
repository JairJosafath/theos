package fake

import (
	"cmp"
	"context"
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
}

var _ ports.TestEnvironments = (*TestEnvironments)(nil)

type envKey struct{ namespace, name string }

type fakeLease struct {
	lease ports.Lease
	req   ports.LeaseRequest
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
