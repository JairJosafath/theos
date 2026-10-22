/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"errors"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// deployWait bounds the wait for a DeployTarget's Ready: the Secret is read
// every second.
const deployWait = 10 * time.Second

func newDeployTarget(ns, name, secret string) *c4v1alpha1.DeployTarget {
	return &c4v1alpha1.DeployTarget{ObjectMeta: objectMeta(ns, name), Spec: c4v1alpha1.DeployTargetSpec{
		GitHub:               &c4v1alpha1.DeployTargetGitHub{Repository: "acme/orders-cli"},
		CredentialsSecretRef: &c4v1alpha1.LocalRef{Name: secret}}}
}

// newArgoCDTarget is an argoCD target with no Secret, its Argo CD in argoNS.
func newArgoCDTarget(ns, name, argoNS, destNS string) *c4v1alpha1.DeployTarget {
	return &c4v1alpha1.DeployTarget{ObjectMeta: objectMeta(ns, name), Spec: c4v1alpha1.DeployTargetSpec{
		ArgoCD: &c4v1alpha1.DeployTargetArgoCD{Namespace: argoNS,
			Destination: c4v1alpha1.DeployTargetArgoCDDestination{Namespace: destNS}}}}
}

func newDeploySecret(ns, name string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: objectMeta(ns, name), Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

// deployReady waits for dt's Ready to have status and reason, and returns it.
func deployReady(dt *c4v1alpha1.DeployTarget, status metav1.ConditionStatus, reason string) *metav1.Condition {
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(dt.Namespace, dt.Name), dt)).To(Succeed())
		cond = condition(dt.Status.Conditions, c4v1alpha1.ConditionDeployTargetReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.ObservedGeneration).To(Equal(dt.Generation))
	}, deployWait).Should(Succeed())
	return cond
}

var _ = Describe("DeployTarget", func() {
	It("defaults its tier to development and leaves approval to the model", func() {
		ns := newProjectNamespace()
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(dt)
		Expect(dt.Spec.Tier).To(Equal(c4v1alpha1.DeployTierDevelopment))
		Expect(dt.Spec.Approval).To(BeEmpty())
	})

	DescribeTable("refuses a spec its CEL rules or schema reject",
		func(mutate func(*c4v1alpha1.DeployTarget), message string) {
			dt := newDeployTarget(newProjectNamespace(), "bad", "bad-github")
			mutate(dt)
			Expect(k8sClient.Create(ctx, dt)).To(MatchError(ContainSubstring(message)))
		},
		Entry("no member", func(dt *c4v1alpha1.DeployTarget) { dt.Spec.GitHub = nil },
			"set exactly one target member: github"),
		Entry("a repository that isn't owner/name", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.Repository = "orders-cli"
		}, "repository must be owner/name"),
		Entry("a repository URL", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.Repository = "https://github.com/acme/orders-cli"
		}, "repository must be owner/name"),
		Entry("more than 50 parameters", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.Parameters = map[string]string{}
			for i := range 51 {
				dt.Spec.Parameters[fmt.Sprintf("p%d", i)] = "v"
			}
		}, "spec.parameters: Too many: 51: must have at most 50 items"),
		Entry("another tier", func(dt *c4v1alpha1.DeployTarget) { dt.Spec.Tier = "staging" },
			`spec.tier: Unsupported value: "staging"`),
		Entry("another approval", func(dt *c4v1alpha1.DeployTarget) { dt.Spec.Approval = "Sometimes" },
			`spec.approval: Unsupported value: "Sometimes"`),
		Entry("an empty Secret name", func(dt *c4v1alpha1.DeployTarget) { dt.Spec.CredentialsSecretRef.Name = "" },
			"spec.credentialsSecretRef.name"),
		// The argoCD member (#1588): a github target keeps its Secret, and a
		// target names one member.
		Entry("a github target with no Secret", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.CredentialsSecretRef = nil
		}, "set credentialsSecretRef unless the member is argoCD"),
		Entry("github and argoCD", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.ArgoCD = &c4v1alpha1.DeployTargetArgoCD{
				Destination: c4v1alpha1.DeployTargetArgoCDDestination{Namespace: "apps"}}
		}, "set exactly one target member: github or argoCD"),
		Entry("an argoCD target with no destination", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub, dt.Spec.CredentialsSecretRef = nil, nil
			dt.Spec.ArgoCD = &c4v1alpha1.DeployTargetArgoCD{}
		}, "spec.argoCD.destination.namespace"),
		// An argoCD target on another cluster (#1590).
		Entry("a gitURL with a path", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub, dt.Spec.CredentialsSecretRef = nil, nil
			dt.Spec.ArgoCD = &c4v1alpha1.DeployTargetArgoCD{GitURL: "http://git.example/path",
				Destination: c4v1alpha1.DeployTargetArgoCDDestination{Namespace: "apps"}}
		}, "gitURL must be scheme://host[:port]"),
		Entry("a gitURL with no host", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub, dt.Spec.CredentialsSecretRef = nil, nil
			dt.Spec.ArgoCD = &c4v1alpha1.DeployTargetArgoCD{GitURL: "http://:3000",
				Destination: c4v1alpha1.DeployTargetArgoCDDestination{Namespace: "apps"}}
		}, "gitURL must be scheme://host[:port]"),
		Entry("an imageRegistry with a scheme", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub, dt.Spec.CredentialsSecretRef = nil, nil
			dt.Spec.ArgoCD = &c4v1alpha1.DeployTargetArgoCD{ImageRegistry: "https://nodes.example",
				Destination: c4v1alpha1.DeployTargetArgoCDDestination{Namespace: "apps"}}
		}, "spec.argoCD.imageRegistry"),
		// The github member's tagPrefix (#1486).
		Entry("a tagPrefix with a space", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.TagPrefix = new("orders cli-")
		}, "spec.github.tagPrefix"),
		Entry("a tagPrefix over 40 characters", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.TagPrefix = new(strings.Repeat("a", 41))
		}, "spec.github.tagPrefix"),
		Entry("a tagPrefix that starts with -", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.TagPrefix = new("-cli-")
		}, "tagPrefix must make a git tag name"),
		Entry("a tagPrefix with ..", func(dt *c4v1alpha1.DeployTarget) {
			dt.Spec.GitHub.TagPrefix = new("cli..")
		}, "tagPrefix must make a git tag name"),
	)

	It("takes an empty tagPrefix, one with a folder, and latest false (#1486)", func() {
		ns := newProjectNamespace()
		dt := newDeployTarget(ns, "bare", "bare-github")
		dt.Spec.GitHub.TagPrefix = new("")
		dt.Spec.GitHub.Latest = new(false)
		folder := newDeployTarget(ns, "folder", "folder-github")
		folder.Spec.GitHub.TagPrefix = new("tools/cli-")
		mustCreate(dt, folder)
		Expect(dt.Spec.GitHub.TagPrefix).To(HaveValue(Equal("")))
		Expect(dt.Spec.GitHub.Latest).To(HaveValue(BeFalse()))
	})

	It("takes 50 parameters and a production tier that needs approval", func() {
		dt := newDeployTarget(newProjectNamespace(), "prod", "prod-github")
		dt.Spec.Tier = c4v1alpha1.DeployTierProduction
		dt.Spec.Approval = c4v1alpha1.DeployApprovalNone
		dt.Spec.Parameters = map[string]string{}
		for i := range 50 {
			dt.Spec.Parameters[fmt.Sprintf("p%d", i)] = "v"
		}
		mustCreate(dt)
	})

	It("is SecretMissing without its Secret, and Ready once the Secret is there", func() {
		ns := newProjectNamespace()
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(dt)
		cond := deployReady(dt, metav1.ConditionFalse, "SecretMissing")
		Expect(cond.Message).To(ContainSubstring("Secret staging-github, with the key token, is missing"))
		Expect(cond.Message).To(ContainSubstring("DeployTarget staging"))
		Expect(dt.Status.CheckedTime).To(BeNil())

		mustCreate(newDeploySecret(ns, "staging-github", map[string]string{"token": "t0ken"}))
		cond = deployReady(dt, metav1.ConditionTrue, "Ready")
		Expect(cond.Message).To(ContainSubstring("GitHub accepts the credential in Secret staging-github for acme/orders-cli"))
		Expect(dt.Status.CheckedTime).NotTo(BeNil())
		Expect(deployFake.Checks(ns + "/staging")).To(Equal(1))
	})

	It("is SecretIncomplete while its Secret lacks the member's key", func() {
		ns := newProjectNamespace()
		secret := newDeploySecret(ns, "staging-github", map[string]string{"username": "me"})
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(secret, dt)
		cond := deployReady(dt, metav1.ConditionFalse, "SecretIncomplete")
		Expect(cond.Message).To(ContainSubstring("Secret staging-github of DeployTarget staging has no key token"))

		Expect(patch(secret, func() { secret.Data["token"] = []byte("t0ken") })).To(Succeed())
		deployReady(dt, metav1.ConditionTrue, "Ready")
	})

	It("names the target and who sets the Secret when the credential is refused", func() {
		ns := newProjectNamespace()
		deployFake.Refuse(ns + "/prod")
		dt := newDeployTarget(ns, "prod", "prod-github")
		mustCreate(newDeploySecret(ns, "prod-github", map[string]string{"token": "wrong"}), dt)
		cond := deployReady(dt, metav1.ConditionFalse, "CredentialRefused")
		Expect(cond.Message).To(ContainSubstring("GitHub refused the credential in Secret prod-github of " +
			"DeployTarget prod for acme/orders-cli"))
		Expect(cond.Message).To(ContainSubstring("Whoever owns DeployTarget prod sets Secret prod-github"))
		Expect(dt.Status.CheckedTime).NotTo(BeNil())
	})

	It("is Unreachable when the target can't be reached", func() {
		ns := newProjectNamespace()
		deployFake.Fail(ns+"/staging", &ports.Error{Kind: ports.ErrUnreachable, Cause: errors.New("HTTP 502")})
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(newDeploySecret(ns, "staging-github", map[string]string{"token": "t0ken"}), dt)
		cond := deployReady(dt, metav1.ConditionFalse, "Unreachable")
		Expect(cond.Message).To(ContainSubstring("GitHub could not be reached to check DeployTarget staging"))
		Expect(cond.Message).To(ContainSubstring("HTTP 502"))

		// Asked again on the next poll, not 10 minutes later.
		deployFake.Fail(ns+"/staging", nil)
		deployReady(dt, metav1.ConditionTrue, "Ready")
	})

	It("is Ready without a Secret when the member is argoCD and its project is there (#1588)", func() {
		ns := newProjectNamespace()
		argoNS := newArgoNamespace()
		dt := newArgoCDTarget(ns, "dev", argoNS, "apps")
		mustCreate(dt)
		Expect(dt.Spec.ArgoCD.Project).To(Equal(model.ArgoCDDefaultProject))
		Expect(dt.Spec.ArgoCD.Destination.Server).To(Equal(model.ArgoCDDefaultServer))
		cond := deployReady(dt, metav1.ConditionFalse, "CredentialRefused")
		Expect(cond.Message).To(ContainSubstring("Argo CD refused the operator's ServiceAccount for DeployTarget dev " +
			"for project default in namespace " + argoNS + " (destination apps): project default doesn't exist"))
		Expect(cond.Message).To(ContainSubstring("Whoever owns DeployTarget dev makes project default in namespace " +
			argoNS))

		// Checked again on a change of the spec, with the project there.
		argoProject(argoNS)
		Expect(patch(dt, func() { dt.Spec.Tier = c4v1alpha1.DeployTierProduction })).To(Succeed())
		cond = deployReady(dt, metav1.ConditionTrue, "Ready")
		Expect(cond.Message).To(ContainSubstring("Argo CD is there for project default in namespace " + argoNS +
			" (destination apps)"))
		Expect(dt.Status.CheckedTime).NotTo(BeNil())
	})

	It("checks again when its Secret or spec changes, and not on every poll", func() {
		ns := newProjectNamespace()
		name := ns + "/staging"
		secret := newDeploySecret(ns, "staging-github", map[string]string{"token": "t0ken"})
		dt := newDeployTarget(ns, "staging", "staging-github")
		mustCreate(secret, dt)
		deployReady(dt, metav1.ConditionTrue, "Ready")
		Expect(deployFake.Checks(name)).To(Equal(1))
		// The Secret is read every second; the target isn't asked again.
		Consistently(func() int { return deployFake.Checks(name) }, 3*time.Second).Should(Equal(1))

		deployFake.Refuse(name)
		Expect(patch(secret, func() { secret.Data["token"] = []byte("another") })).To(Succeed())
		deployReady(dt, metav1.ConditionFalse, "CredentialRefused")
		Expect(deployFake.Checks(name)).To(Equal(2))

		deployFake.Fail(name, nil)
		Expect(patch(dt, func() { dt.Spec.Tier = c4v1alpha1.DeployTierProduction })).To(Succeed())
		deployReady(dt, metav1.ConditionTrue, "Ready")
		Expect(deployFake.Checks(name)).To(Equal(3))
	})
})
