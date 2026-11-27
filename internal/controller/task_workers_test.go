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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

var _ = Describe("The operator's Workers", func() {
	// #336: the Sandboxes as ports.Workers keep the contract internal/engine
	// relies on against the API server, under the operator's generated role:
	// the API server's UIDs tell a turn offered afresh from the Sandbox it
	// replaced, and the role grants every call. The contract takes a
	// *testing.T, so it runs as a subtest of the suite's own.
	It("keeps the Workers contract on Sandboxes", func() {
		// The Workers read live, as the contract reads each call back at once.
		operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		r := &TaskReconciler{Client: operator, APIReader: operator, Scheme: scheme.Scheme}
		// One project per subtest, made here: the contract's callbacks run in
		// the subtests' goroutines, where a Gomega failure can't be reported.
		var projects []string
		for range 2 {
			projects = append(projects, newProjectNamespace())
		}
		env := sandboxWorkersEnv(r, func() client.Client { return k8sClient }, func(t *testing.T) *c4v1beta1.Task {
			if len(projects) == 0 {
				t.Fatal("no project left for another subtest of the contract")
			}
			task := workersTask(projects[0])
			projects = projects[1:]
			if err := k8sClient.Create(ctx, task); err != nil {
				t.Fatal(err)
			}
			return task
		})
		Expect(suiteT.Run("SandboxWorkersContract336", func(t *testing.T) { contract.Workers(t, env) })).
			To(BeTrue(), "contract.Workers on the operator's Sandboxes; its subtests' output says what failed")
	})
})
