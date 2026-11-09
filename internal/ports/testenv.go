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

package ports

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TestEnvironments leases places to run commands with tools the agent
// sandbox lacks: a browser, a phone emulator, a GPU
// (docs/plans/done/test-environments.md, T6). One provider serves one member of
// TestEnvironment.spec; cmd/main.go maps each member's name to its adapter,
// as it does for DeployTargets. A lease runs `theos-env serve`, and the run
// that holds it reaches it at Endpoint with the token it was leased with.
type TestEnvironments interface {
	// Lease takes a place for req, or returns the one taken before for its
	// ID: repeating it starts nothing new. The lease may be Pending; Get
	// says when it is Ready.
	Lease(ctx context.Context, req LeaseRequest) (Lease, error)
	// Get reads lease id of env back; ErrLeaseNotFound when the provider
	// holds none.
	Get(ctx context.Context, env Environment, id string) (Lease, error)
	// Return ends lease id of env and frees what it held. Returning one that
	// is gone is no error.
	Return(ctx context.Context, env Environment, id string) error
	// Leases lists the leases the provider holds for env, the ones the
	// operator lost track of included, so it can return them.
	Leases(ctx context.Context, env Environment) ([]Lease, error)
}

// TestEnvironmentProviders are the providers by member name, as cmd/main.go
// builds them.
type TestEnvironmentProviders map[string]TestEnvironments

// For returns the provider of member.
func (p TestEnvironmentProviders) For(member string) (TestEnvironments, error) {
	if provider, ok := p[member]; ok && provider != nil {
		return provider, nil
	}
	return nil, fmt.Errorf("no provider for test environment member %q", member)
}

// ErrLeaseNotFound is Get's error for a lease the provider doesn't hold.
var ErrLeaseNotFound = errors.New("no such lease")

// Environment is a TestEnvironment as a provider reads it.
type Environment struct {
	// Namespace and Name are the TestEnvironment's, and UID its UID: a local
	// lease lives in Namespace and is owned by it.
	Namespace, Name, UID string
	// Member is the provider's member name, such as local.
	Member string
	// Local is the local member's settings; nil for another member.
	Local *LocalEnvironment
	// AWS is the aws member's settings; nil for another member.
	AWS *AWSEnvironment
	// DeviceFarm is the deviceFarm member's settings; nil for another
	// member.
	DeviceFarm *DeviceFarmEnvironment
	// Secret is the data of a remote member's credentials Secret, read by
	// the operator only; never sent to the environment.
	Secret map[string][]byte
}

// AWSEnvironment is the aws member: an EC2 machine per lease (T11).
type AWSEnvironment struct {
	// Region is where the machines run; Account, when set, the AWS account
	// the credential must be in.
	Region, Account string
	// InstanceType and ImageID are what the machines are, and SubnetID where
	// they start.
	InstanceType, ImageID, SubnetID string
	// KeepHostHours is how long an EC2 Mac's dedicated host is kept from its
	// allocation for the leases that follow; 0 is
	// model.DefaultKeepHostHours. Unused for other instance types.
	KeepHostHours int
}

// TestEnvironmentCredentials is the optional capability of a remote
// provider to check a credential without leasing: whether it takes the
// credential in env.Secret, in the account env names (T9). An error is an
// *Error of ErrCredentialRefused or ErrUnreachable where it is one.
type TestEnvironmentCredentials interface {
	CheckCredentials(ctx context.Context, env Environment) error
}

// TestEnvironmentReleaser is the optional capability of a provider that
// keeps something for an environment beside its leases, such as a remote
// member's security group: Release frees it once the environment is
// deleted and its leases are returned. Releasing what is gone is no error.
type TestEnvironmentReleaser interface {
	Release(ctx context.Context, env Environment) error
}

// LocalEnvironment is the local member: a pod per lease on the cluster.
type LocalEnvironment struct {
	// Image has the environment's tools, pinned.
	Image string
	// Requests and Limits are the container's resources by name, such as
	// cpu: 2; Devices the extended resources it requests and is limited to,
	// such as devices.kubevirt.io/kvm: 1.
	Requests, Limits, Devices map[string]string
	// RuntimeClassName, NodeSelector and Tolerations place the pod.
	RuntimeClassName string
	NodeSelector     map[string]string
	Tolerations      []Toleration
	// PullSecrets are the Secrets the pod pulls its images with, the shared
	// ones the operator copies into the namespace.
	PullSecrets []string
}

// Toleration is a pod's toleration of a node taint.
type Toleration struct {
	Key, Operator, Value, Effect string
	// Seconds is how long a NoExecute taint is tolerated; nil for ever.
	Seconds *int64
}

// LeaseRequest asks for one lease.
type LeaseRequest struct {
	// ID names the lease (model.LeaseID): a second request with it gets the
	// same lease.
	ID string
	// Env is the environment to lease.
	Env Environment
	// Holder is the Task whose run holds the lease.
	Holder string
	// Token is the lease's own token: the environment takes requests with it
	// alone, so one run can't use another's lease (T5).
	Token string
	// Deadline is how long the lease lasts at most; the provider ends it
	// then.
	Deadline time.Duration
}

// Lease is one place a provider holds for a run.
type Lease struct {
	ID string
	// State is model.LeasePending, LeaseReady, LeaseFailed or LeaseEnded.
	State string
	// Endpoint is where theos-env serve answers, http(s)://host:port, once
	// Ready.
	Endpoint string
	// CA is the PEM certificate an https Endpoint is pinned to; "" for an
	// endpoint inside the cluster, which the fence guards.
	CA string
	// Message says why a lease is Pending or Failed.
	Message string
	// Holder is the request's Holder.
	Holder string
	// Started is when the provider took the place.
	Started time.Time
	// CostMilliUSD is what the lease cost so far.
	CostMilliUSD int64
}
