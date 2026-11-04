// Package fake holds the ports' fakes, for the controllers' tests and the
// contract suites.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/JairJosafath/theos/internal/ports"
)

// Deployer is a ports.Deployer in memory. It records the deploys it starts,
// reports what a test sets, and fails on demand. A shipment is deployed and
// healthy at once unless Hold is set; then it stays Deploying until the test
// sets its state. Like GitHub's, it needs at least one file.
type Deployer struct {
	// Hold keeps each new deploy Deploying.
	Hold bool

	mu sync.Mutex
	// held are the targets, by name, whose new deploys stay Deploying.
	held map[string]bool
	// refused are the targets, by name, that refuse their credential; fail
	// the error each other target's calls fail with.
	refused map[string]bool
	// unprepared are the targets, by name, whose Ship fails
	// ErrPrerequisiteMissing.
	unprepared map[string]bool
	fail       map[string]error
	states     map[key]ports.DeployState
	shipped    []Shipped
	checks     map[string]int
	// blocked are the targets, by name, whose Ship waits until the gate
	// closes; inShip counts the calls waiting there.
	blocked map[string]chan struct{}
	inShip  map[string]int
}

var _ ports.Deployer = (*Deployer)(nil)

type key struct{ target, app string }

// Shipped is a deploy the fake started.
type Shipped struct {
	Target   string
	Shipment ports.Shipment
}

// Refuse makes target refuse its credential, in Check and Ship.
func (d *Deployer) Refuse(target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refused == nil {
		d.refused = map[string]bool{}
	}
	d.refused[target] = true
}

// Unprepare makes target lack a prerequisite: its Ship fails
// ErrPrerequisiteMissing and ships nothing. Fail(target, nil) clears it.
func (d *Deployer) Unprepare(target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unprepared == nil {
		d.unprepared = map[string]bool{}
	}
	d.unprepared[target] = true
}

// Fail makes every call for target fail with err; nil clears it, Refuse and
// Unprepare.
func (d *Deployer) Fail(target string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail == nil {
		d.fail = map[string]error{}
	}
	d.fail[target] = err
	if err == nil {
		delete(d.fail, target)
		delete(d.refused, target)
		delete(d.unprepared, target)
	}
}

// HoldAt keeps each new deploy at target Deploying, as Hold does everywhere.
func (d *Deployer) HoldAt(target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.held == nil {
		d.held = map[string]bool{}
	}
	d.held[target] = true
}

// Block makes each Ship to target wait, as a long upload does, until the
// returned release is called or the call's context ends.
func (d *Deployer) Block(target string) (release func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.blocked == nil {
		d.blocked = map[string]chan struct{}{}
	}
	gate := make(chan struct{})
	d.blocked[target] = gate
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			delete(d.blocked, target)
			close(gate)
		})
	}
}

// Blocked counts the Ship calls to target waiting on its Block.
func (d *Deployer) Blocked(target string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inShip[target]
}

// wait holds a Ship to target while it is blocked.
func (d *Deployer) wait(ctx context.Context, target string) error {
	d.mu.Lock()
	gate := d.blocked[target]
	if gate == nil {
		d.mu.Unlock()
		return nil
	}
	if d.inShip == nil {
		d.inShip = map[string]int{}
	}
	d.inShip[target]++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.inShip[target]--
		d.mu.Unlock()
	}()
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetState sets what target reports for app.
func (d *Deployer) SetState(target, app string, st ports.DeployState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.states == nil {
		d.states = map[key]ports.DeployState{}
	}
	d.states[key{target, app}] = st
}

// Shipments returns the deploys started, in order.
func (d *Deployer) Shipments() []Shipped {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Shipped(nil), d.shipped...)
}

// Checks counts the Check calls for target.
func (d *Deployer) Checks(target string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checks[target]
}

// Deploys counts the deploys of app started at target.
func (d *Deployer) Deploys(target, app string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, s := range d.shipped {
		if s.Target == target && s.Shipment.App == app {
			n++
		}
	}
	return n
}

// failure is the error target's calls fail with, under d's lock.
func (d *Deployer) failure(target string) error {
	if err := d.fail[target]; err != nil {
		return err
	}
	if d.refused[target] {
		return &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("fake: %s refuses its credential", target)}
	}
	return nil
}

// Check fails as set, else accepts.
func (d *Deployer) Check(_ context.Context, t ports.Target) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.checks == nil {
		d.checks = map[string]int{}
	}
	d.checks[t.Name]++
	return d.failure(t.Name)
}

// Ship starts a deploy of s unless the newest deploy of its app at t is of
// its version and hasn't failed. A blocked target's Ship waits first.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	if err := d.wait(ctx, t.Name); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.failure(t.Name); err != nil {
		return err
	}
	if d.unprepared[t.Name] {
		return &ports.Error{Kind: ports.ErrPrerequisiteMissing,
			Cause: fmt.Errorf("fake: %s lacks a prerequisite", t.Name)}
	}
	if len(s.Files) == 0 {
		return &ports.Error{Kind: ports.ErrArtifactMissing,
			Cause: fmt.Errorf("fake: release %s of %s has no files", s.Version, s.App)}
	}
	k := key{t.Name, s.App}
	if st, ok := d.states[k]; ok && st.Version == s.Version && st.Phase != ports.PhaseFailed {
		return nil
	}
	d.shipped = append(d.shipped, Shipped{Target: t.Name, Shipment: s})
	st := ports.DeployState{Version: s.Version, Phase: ports.PhaseDeployed, Health: ports.HealthHealthy}
	if d.Hold || d.held[t.Name] {
		st = ports.DeployState{Version: s.Version, Phase: ports.PhaseDeploying, Health: ports.HealthProgressing}
	}
	if d.states == nil {
		d.states = map[key]ports.DeployState{}
	}
	d.states[k] = st
	return nil
}

// State reports what was set or shipped last: the fake runs one version at
// a time, so it ignores the wanted version.
func (d *Deployer) State(_ context.Context, t ports.Target, app, _ string) (ports.DeployState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.failure(t.Name); err != nil {
		return ports.DeployState{}, err
	}
	return d.states[key{t.Name, app}], nil
}
