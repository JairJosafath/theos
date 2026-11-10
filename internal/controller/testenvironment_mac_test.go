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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// newMacEnvironment is an aws TestEnvironment ios in ns on an EC2 Mac.
func newMacEnvironment(ns string) *c4v1beta1.TestEnvironment {
	env := newGPUEnvironment(ns)
	env.Name = "ios"
	env.Spec.Description = "macOS with Xcode and the iOS Simulator"
	env.Spec.AWS.InstanceType = "mac2.metal"
	env.Spec.Limits.BudgetUSDPerDay = 25
	return env
}

// docs/plans/done/test-environments.md, step 9: an EC2 Mac, whose dedicated
// hosts the aws provider keeps (tested on a fake EC2 in
// internal/adapters/awsenv).
var _ = Describe("TestEnvironment on an EC2 Mac", func() {
	It("takes a Mac instance type, keeps its hosts 24 hours by default, and gives the provider the setting", func() {
		ns := newProjectNamespace()
		env := newMacEnvironment(ns)
		env.Spec.AWS.Mac = &c4v1beta1.TestEnvironmentMac{}
		mustCreate(env)
		Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
		Expect(env.Spec.AWS.Mac.KeepHostHours).To(Equal(int32(24)))
		Expect(patch(env, func() { env.Spec.AWS.Mac.KeepHostHours = 72 })).To(Succeed())
		Expect(testEnvironment(env, nil).AWS).To(Equal(&ports.AWSEnvironment{Region: "eu-west-1",
			Account: "123456789012", InstanceType: "mac2.metal", ImageID: "ami-0123456789abcdef0",
			SubnetID: "subnet-0123456789abcdef0", KeepHostHours: 72}))
	})

	// The CI runner a machine downloads is Linux's, and a host per job
	// would be billed 24 hours each: CEL refuses a Mac's ci (steps 8, 9).
	It("refuses ci on an EC2 Mac", func() {
		env := newMacEnvironment(newProjectNamespace())
		env.Spec.CI = &c4v1beta1.TestEnvironmentCI{}
		Expect(k8sClient.Create(ctx, env)).To(MatchError(ContainSubstring("an EC2 Mac runs no CI jobs")))
	})
})

// A deleted Mac environment waits a host's 24 hours to be released, since EC2
// releases no Mac host within them; any other gives up after 15 minutes.
func TestTestEnvReleaseWithin(t *testing.T) {
	mac, gpu := newMacEnvironment("apps"), newGPUEnvironment("apps")
	if got := testEnvReleaseWithin(mac); got != model.MacHostMinimum+15*time.Minute {
		t.Errorf("a Mac waits %v, want a host's 24 hours and 15 minutes", got)
	}
	if got := testEnvReleaseWithin(gpu); got != 15*time.Minute {
		t.Errorf("a GPU machine waits %v, want 15 minutes", got)
	}
	if got := testEnvReleaseWithin(newBrowserEnvironment("apps")); got != 15*time.Minute {
		t.Errorf("a local environment waits %v, want 15 minutes", got)
	}
}
