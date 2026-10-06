/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// Every reconciler in this suite runs as the operator's ServiceAccount, bound to
// config/rbac/role.yaml as deployed; a call the role doesn't grant fails the spec
// that needs it. This spec shows the suite really runs under that role.
var _ = Describe("The operator's RBAC", func() {
	It("is the generated role, not the test's admin", func() {
		manager, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.List(ctx, &c4v1alpha1.ProjectList{})).To(Succeed())
		err = manager.List(ctx, &corev1.NodeList{})
		Expect(apierrors.IsForbidden(err)).To(BeTrue(), "listing nodes: %v", err)
		Expect(err.Error()).To(ContainSubstring(managerUser))
	})
})
