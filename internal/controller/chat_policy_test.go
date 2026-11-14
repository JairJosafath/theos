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
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// chatUser is theos-chat's ServiceAccount in config/chat/rbac.yaml.
const chatUser = "system:serviceaccount:theos-system:theos-chat"

// The roles a person gets in these specs: editing Chats, so they add
// themselves; and writing Tasks, so their word from chat lands.
const (
	roleChatPeople = "test-chat-people"
	roleTaskWord   = "test-task-word"
)

var (
	personRolesOnce sync.Once
	chatCfgOnce     sync.Once
	chatCfg         *rest.Config
)

// asUser is a client of the API server as user and groups, by the admin's
// impersonation: the API server sees that user, and their RBAC decides.
func asUser(user string, groups ...string) client.Client {
	GinkgoHelper()
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
	cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	return cl
}

// grant binds the person roles named to user, cluster-wide.
func grant(user string, roles ...string) {
	GinkgoHelper()
	personRolesOnce.Do(func() {
		for name, resource := range map[string]string{roleChatPeople: "chats", roleTaskWord: "tasks"} {
			mustCreate(&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{c4v1beta1.GroupVersion.Group}, Resources: []string{resource},
				Verbs: []string{"get", "list", "watch", "create", "update", "patch"}}}})
		}
	})
	for _, role := range roles {
		name := role + "-" + strings.NewReplacer("@", "-at-", ".", "-").Replace(user)
		binding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name},
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role},
			Subjects: []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: user}}}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, binding))).To(Succeed())
	}
}

// revoke takes the person role named from user.
func revoke(user, role string) {
	GinkgoHelper()
	name := role + "-" + strings.NewReplacer("@", "-at-", ".", "-").Replace(user)
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name}}))).To(Succeed())
}

// theosChatConfig is theos-chat's own rest.Config: a client certificate of
// its ServiceAccount's user, bound to config/chat/rbac.yaml's role, so it
// may impersonate only as that role allows.
func theosChatConfig() *rest.Config {
	GinkgoHelper()
	chatCfgOnce.Do(func() {
		u, err := testEnv.AddUser(envtest.User{Name: chatUser,
			Groups: []string{"system:serviceaccounts", "system:serviceaccounts:theos-system"}}, cfg)
		Expect(err).NotTo(HaveOccurred())
		chatCfg = u.Config()
	})
	return chatCfg
}

// chatWord is a merge patch setting one chat annotation and its -by.
func chatWord(task *c4v1beta1.Task, key, value, by string) func() {
	return func() {
		if task.Annotations == nil {
			task.Annotations = map[string]string{}
		}
		task.Annotations[key], task.Annotations[model.ChatAnnotationBy(key)] = value, by
	}
}

// wordAs writes one chat annotation and its -by on a copy of task, as c's
// user.
func wordAs(c client.Client, task *c4v1beta1.Task, key, value, by string) error {
	t := task.DeepCopy()
	return patchAs(c, t, chatWord(t, key, value, by))
}

// patchAs sends obj's change by mutate as a merge patch, as c's user.
func patchAs(c client.Client, obj client.Object, mutate func()) error {
	orig, _ := obj.DeepCopyObject().(client.Object)
	mutate()
	return c.Patch(ctx, obj, client.MergeFrom(orig))
}

// The admission policies that keep a person's word from chat the person's
// own (config/policy, docs/plans/done/chat-matrix.md).
var _ = Describe("Chat admission policies", func() {
	It("lets each person map only themselves into a Chat's people", func() {
		ns := newProjectNamespace()
		ada, bob := "ada-"+ns+"@example.com", "bob-"+ns+"@example.com"
		grant(ada, roleChatPeople)
		grant(bob, roleChatPeople)
		adaEntry := c4v1beta1.ChatPerson{MatrixID: "@ada:example.org", User: ada, Groups: []string{"shop"}}

		By("refusing an admin who maps someone else")
		chat := newChat(ns, "chat", "matrix-bot", adaEntry)
		Eventually(func() error { return k8sClient.Create(ctx, chat.DeepCopy()) }).Should(MatchError(And(
			ContainSubstring("spec.people may name only the writer, admin"),
			ContainSubstring("@ada:example.org as "+ada))))

		By("refusing a group the writer isn't in")
		Expect(asUser(ada).Create(ctx, chat.DeepCopy())).To(MatchError(ContainSubstring(
			"groups the writer is in")))

		By("taking a person who maps themselves, with their groups")
		Expect(asUser(ada, "shop").Create(ctx, chat)).To(Succeed())

		By("taking another person who adds themselves, ada's entry kept as it was")
		bobEntry := c4v1beta1.ChatPerson{MatrixID: "@bob:example.org", User: bob}
		Expect(patchAs(asUser(bob), chat, func() { chat.Spec.People = append(chat.Spec.People, bobEntry) })).
			To(Succeed())

		By("refusing a person who maps a Matrix account to someone else")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patchAs(asUser(bob), chat, func() {
			chat.Spec.People = append(chat.Spec.People, c4v1beta1.ChatPerson{MatrixID: "@mallory:example.org", User: ada})
		})).To(MatchError(ContainSubstring("@mallory:example.org as " + ada)))

		By("refusing a person who changes someone else's groups")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patchAs(asUser(bob), chat, func() { chat.Spec.People[0].Groups = []string{"admins"} })).
			To(MatchError(ContainSubstring("@ada:example.org as " + ada)))

		By("taking an admin's change beside the people, who are as they were")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patch(chat, func() { chat.Spec.Space = "The shop" })).To(Succeed())
		Expect(chat.Spec.People).To(Equal([]c4v1beta1.ChatPerson{adaEntry, bobEntry}))
	})

	It("lets each person map only themselves into a Slack Chat's people (#158)", func() {
		ns := newProjectNamespace()
		ada, bob := "ada-"+ns+"@example.com", "bob-"+ns+"@example.com"
		grant(ada, roleChatPeople)
		grant(bob, roleChatPeople)
		adaEntry := c4v1beta1.ChatSlackPerson{SlackID: "U0ADA", User: ada, Groups: []string{"shop"}}
		chat := newSlackChat(ns, "chat", "slack-bot")
		chat.Spec.Slack = &c4v1beta1.ChatSlack{People: []c4v1beta1.ChatSlackPerson{adaEntry}}

		By("refusing an admin who maps someone else")
		Eventually(func() error { return k8sClient.Create(ctx, chat.DeepCopy()) }).Should(MatchError(And(
			ContainSubstring("spec.slack.people may name only the writer, admin"),
			ContainSubstring("U0ADA as "+ada))))

		By("refusing a group the writer isn't in")
		Expect(asUser(ada).Create(ctx, chat.DeepCopy())).To(MatchError(ContainSubstring(
			"groups the writer is in")))

		By("taking a person who maps themselves, with their groups")
		Expect(asUser(ada, "shop").Create(ctx, chat)).To(Succeed())

		By("taking another person who adds themselves, ada's entry kept as it was")
		bobEntry := c4v1beta1.ChatSlackPerson{SlackID: "W0BOB", User: bob}
		Expect(patchAs(asUser(bob), chat, func() { chat.Spec.Slack.People = append(chat.Spec.Slack.People, bobEntry) })).
			To(Succeed())

		By("refusing a person who maps a Slack member to someone else")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patchAs(asUser(bob), chat, func() {
			chat.Spec.Slack.People = append(chat.Spec.Slack.People, c4v1beta1.ChatSlackPerson{SlackID: "U0MALLORY", User: ada})
		})).To(MatchError(ContainSubstring("U0MALLORY as " + ada)))

		By("refusing a person who changes someone else's groups")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patchAs(asUser(bob), chat, func() { chat.Spec.Slack.People[0].Groups = []string{"admins"} })).
			To(MatchError(ContainSubstring("U0ADA as " + ada)))

		By("taking an admin's change beside the people, who are as they were")
		Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
		Expect(patch(chat, func() { chat.Spec.DigestMinutes = 5 })).To(Succeed())
		Expect(chat.Spec.Slack.People).To(Equal([]c4v1beta1.ChatSlackPerson{adaEntry, bobEntry}))
	})

	It("lets only a person write a chat annotation, and only in their own name", func() {
		ns := newProjectNamespace()
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleTaskWord)
		task := newSpecTask(ns, "orders-api-4-spec")
		mustCreate(task)
		approve := model.ChatAnnotationApproveSpec

		By("refusing the operator's ServiceAccount")
		operator := asUser(managerUser)
		Eventually(func() error {
			return wordAs(operator, task, approve, "abc", managerUser)
		}).Should(MatchError(ContainSubstring(managerUser + " may not write theos.factory.io/approve-spec")))

		By("refusing theos-chat's ServiceAccount acting as itself, even were it let write Tasks")
		grant(chatUser, roleTaskWord)
		DeferCleanup(revoke, chatUser, roleTaskWord)
		Eventually(func() error {
			return wordAs(asUser(chatUser), task, approve, "abc", chatUser)
		}).Should(MatchError(ContainSubstring(chatUser + " may not write theos.factory.io/approve-spec")))

		By("refusing a person who names nobody, or someone else")
		person := asUser(ada)
		bare := task.DeepCopy()
		Expect(patchAs(person, bare, func() { bare.Annotations = map[string]string{approve: "abc"} })).
			To(MatchError(ContainSubstring("needs its -by annotation naming who writes it, " + ada)))
		Expect(k8sClient.Get(ctx, key(ns, task.Name), task)).To(Succeed())
		Expect(patchAs(person, task, chatWord(task, approve, "abc", "bob@example.com"))).
			To(MatchError(ContainSubstring("needs its -by annotation naming who writes it, " + ada)))

		By("taking a person's word in their own name")
		Expect(k8sClient.Get(ctx, key(ns, task.Name), task)).To(Succeed())
		Expect(patchAs(person, task, chatWord(task, approve, "abc", ada))).To(Succeed())
		Expect(task.Annotations).To(HaveKeyWithValue(model.ChatAnnotationBy(approve), ada))

		By("refusing anyone who changes only its -by, or removes it")
		Expect(wordAs(person, task, approve, "abc", "bob@example.com")).
			To(MatchError(ContainSubstring("needs its -by annotation")))
		Expect(patchAs(operator, task.DeepCopy(), func() {})).To(Succeed(), "an empty patch changes nothing")
		Expect(patchAs(operator, task, func() { delete(task.Annotations, approve) })).
			To(MatchError(ContainSubstring("may not write theos.factory.io/approve-spec")))

		By("taking the operator's writes that leave them as they are")
		Expect(k8sClient.Get(ctx, key(ns, task.Name), task)).To(Succeed())
		Expect(patchAs(operator, task, func() { task.Labels = map[string]string{"theos.factory.io/x": "y"} })).
			To(Succeed())

		By("refusing a Task made with one")
		made := newSpecTask(ns, "orders-api-5-spec")
		made.Annotations = map[string]string{model.ChatAnnotationAnswer: "x", model.ChatAnnotationBy(model.ChatAnnotationAnswer): "x"}
		err := operator.Create(ctx, made)
		Expect(apierrors.IsForbidden(err) || apierrors.IsInvalid(err)).To(BeTrue(), "%v", err)
		Expect(err).To(MatchError(ContainSubstring("may not write theos.factory.io/answer")))
	})
})
