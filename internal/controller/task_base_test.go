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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// #1892: round 1 starts only on a green base.
var _ = Describe("Task before round 1", func() {
	const push = "check / check (push)"
	setMain := func(ns, state string) {
		gitFake.SetCheckContexts(ns, "orders-api", ports.DefaultBranch, state, map[string]string{push: state})
	}
	waitsWith := func(ns, reason, message string) {
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "orders-api-1").Status.Conditions, ConditionProgressing)
			g.Expect(cond).To(PointTo(MatchFields(IgnoreExtras, Fields{"Status": Equal(metav1.ConditionTrue),
				"Reason": Equal(reason), "Message": ContainSubstring(message)})))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-implement"), newSandbox()))).
			To(BeTrue(), "no round starts")
	}
	start := func(ns, state string) {
		readyContainer(ns)
		setMain(ns, state)
		mustCreate(newAgent(ns, 3), newTask(ns, "orders-api-1", "Add orders"))
	}

	It("waits with reason BaseRed while main is red, and starts once it is green", func() {
		ns := newProjectNamespace()
		start(ns, "failure")
		waitsWith(ns, model.ReasonBaseRed, "Main's checks failed")
		setMain(ns, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	})

	It("waits while main's checks run, and starts once they pass", func() {
		ns := newProjectNamespace()
		start(ns, "pending")
		waitsWith(ns, "Waiting", "main's checks")
		setMain(ns, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	})

	It("starts at once on a green main", func() {
		ns := newProjectNamespace()
		start(ns, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	})
})
