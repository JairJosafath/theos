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
	"crypto/rand"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/model"
)

// newChat is a Chat on chatFake whose bot's token is in Secret secret.
func newChat(ns, name, secret string, people ...c4v1beta1.ChatPerson) *c4v1beta1.Chat {
	return &c4v1beta1.Chat{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.ChatSpec{
		Homeserver: chatFake.URL(), CredentialsSecretRef: c4v1beta1.LocalRef{Name: secret}, People: people}}
}

// chatBot makes a bot on chatFake and its Secret matrix-bot in ns, and
// returns the bot's user ID.
func chatBot(ns string) string {
	GinkgoHelper()
	bot, token := chatFake.NewUser("bot-" + ns)
	mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "matrix-bot"),
		Data: map[string][]byte{model.ChatTokenKey: []byte(token)}})
	return bot
}

// chatReady waits for chat's Ready to have status and reason, and returns it.
func chatReady(chat *c4v1beta1.Chat, status metav1.ConditionStatus, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(chat.Namespace, chat.Name), chat)).To(Succeed())
		cond = condition(chat.Status.Conditions, c4v1beta1.ConditionChatReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.ObservedGeneration).To(Equal(chat.Generation))
	}).Should(Succeed())
	return cond
}

// chatRoomIDs are the Space's and the rooms' IDs in chat's status.
func chatRoomIDs(chat *c4v1beta1.Chat) []string {
	ids := make([]string, 0, 1+len(chat.Status.Rooms))
	ids = append(ids, chat.Status.Space.ID)
	for _, r := range chat.Status.Rooms {
		ids = append(ids, r.ID)
	}
	return ids
}

var _ = Describe("Chat", func() {
	It("makes its Space and six rooms once, invites its people and reports Ready", func() {
		ns := newProjectNamespace()
		bot := chatBot(ns)
		ada, _ := chatFake.NewUser("ada-" + ns)
		// Each person adds themselves (config/policy/chat_people_policy.yaml).
		adaUser, bobUser := "ada-"+ns+"@example.com", "bob-"+ns+"@example.com"
		grant(adaUser, roleChatPeople)
		grant(bobUser, roleChatPeople)
		chat := newChat(ns, "chat", "matrix-bot", c4v1beta1.ChatPerson{MatrixID: ada, User: adaUser,
			Groups: []string{"shop-team"}})
		chat.Spec.Rooms.General = "lobby"
		Expect(asUser(adaUser, "shop-team").Create(ctx, chat)).To(Succeed())
		Expect(chat.Spec.Rooms.Updates).To(Equal("updates"), "the CRD names each room after its purpose")
		Expect(chat.Spec.DigestMinutes).To(Equal(int32(10)))

		cond := chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		prefix := "#theos-" + ns
		Expect(cond.Message).To(Equal("Space " + prefix + ":" + matrixtest.ServerName + " and its 6 rooms are ready on " +
			chatFake.URL() + "; 1 person invited"))
		Expect(chat.Status.Homeserver).To(Equal(chatFake.URL()))
		Expect(chat.Status.Bot).To(Equal(bot))
		Expect(chat.Status.Invited).To(Equal([]string{ada}))
		Expect(chat.Status.Space.Alias).To(Equal(prefix + ":" + matrixtest.ServerName))
		space, ok := chatFake.Room(chat.Status.Space.ID)
		Expect(ok).To(BeTrue())
		Expect(space.Space).To(BeTrue())
		Expect(space.Name).To(Equal("Test story"), "the Space is named after the Project")
		Expect(space.JoinRule).To(Equal("invite"))
		Expect(space.Members).To(HaveKeyWithValue(ada, "invite"))

		Expect(chat.Status.Rooms).To(HaveLen(6))
		children := make([]string, 0, len(model.ChatPurposes))
		for i, purpose := range model.ChatPurposes {
			status := chat.Status.Rooms[i]
			Expect(status.Purpose).To(Equal(c4v1beta1.ChatPurpose(purpose)))
			Expect(status.Alias).To(Equal(prefix + "-" + string(purpose) + ":" + matrixtest.ServerName))
			room, ok := chatFake.Room(status.ID)
			Expect(ok).To(BeTrue())
			want := string(purpose)
			if purpose == model.ChatGeneral {
				want = "lobby"
			}
			Expect(room.Name).To(Equal(want))
			Expect(room.Topic).To(Equal(model.ChatTopic(purpose)))
			Expect(room.Alias).To(Equal(status.Alias))
			Expect(room.Parents).To(Equal([]string{chat.Status.Space.ID}))
			Expect(room.JoinRule).To(Equal("invite"))
			Expect(room.Encrypted).To(BeFalse())
			Expect(room.Members).To(HaveKeyWithValue(ada, "invite"))
			children = append(children, status.ID)
		}
		slices.Sort(children)
		Expect(space.Children).To(Equal(children))

		By("making nothing on the next reconciles, and inviting a person added")
		rooms, made := chatFake.Rooms(), chatFake.Calls("createRoom")
		ids := chatRoomIDs(chat)
		bob, _ := chatFake.NewUser("bob-" + ns)
		Expect(patchAs(asUser(bobUser), chat, func() {
			chat.Spec.People = append(chat.Spec.People, c4v1beta1.ChatPerson{MatrixID: bob, User: bobUser})
		})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
			g.Expect(chat.Status.Invited).To(ConsistOf(ada, bob))
			g.Expect(condition(chat.Status.Conditions, c4v1beta1.ConditionChatReady).ObservedGeneration).
				To(Equal(chat.Generation))
		}).Should(Succeed())
		for _, id := range ids {
			room, _ := chatFake.Room(id)
			Expect(room.Members).To(HaveKeyWithValue(bob, "invite"))
		}
		Expect(chatFake.Rooms()).To(Equal(rooms))
		Expect(chatFake.Calls("createRoom")).To(Equal(made))

		By("finding its rooms by alias when status has lost their IDs, as after a restore")
		Eventually(func() error {
			if err := k8sClient.Get(ctx, key(ns, "chat"), chat); err != nil {
				return err
			}
			chat.Status.Space, chat.Status.Rooms = nil, nil
			return k8sClient.Status().Update(ctx, chat)
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
			g.Expect(chat.Status.Space).NotTo(BeNil())
			g.Expect(chatRoomIDs(chat)).To(Equal(ids))
		}).Should(Succeed())
		Expect(chatFake.Rooms()).To(Equal(rooms))
		Expect(chatFake.Calls("createRoom")).To(Equal(made))
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
	})

	It("reports a missing Secret, then makes the rooms once it exists", func() {
		ns := newProjectNamespace()
		chat := newChat(ns, "chat", "matrix-bot")
		mustCreate(chat)
		cond := chatReady(chat, metav1.ConditionFalse, model.ChatSecretMissing)
		Expect(cond.Message).To(ContainSubstring("Secret matrix-bot is missing"))
		Expect(cond.Message).To(ContainSubstring("under accessToken"))

		chatBot(ns)
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(chat.Status.Rooms).To(HaveLen(6))
	})

	It("reports a Secret without the token, a token refused and no homeserver", func() {
		ns := newProjectNamespace()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "empty"), Data: map[string][]byte{"token": []byte("x")}})
		incomplete := newChat(ns, "incomplete", "empty")
		mustCreate(incomplete)
		Expect(chatReady(incomplete, metav1.ConditionFalse, model.ChatSecretIncomplete).Message).
			To(ContainSubstring("Secret empty has no accessToken"))

		ns = newProjectNamespace()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "stale"),
			Data: map[string][]byte{model.ChatTokenKey: []byte(rand.Text())}})
		refused := newChat(ns, "refused", "stale")
		mustCreate(refused)
		Expect(chatReady(refused, metav1.ConditionFalse, model.ChatCredentialRefused).Message).
			To(ContainSubstring("refused the access token in Secret stale"))

		ns = newProjectNamespace()
		chatBot(ns)
		unset := newChat(ns, "unset", "matrix-bot")
		unset.Spec.Homeserver = ""
		mustCreate(unset)
		Expect(chatReady(unset, metav1.ConditionFalse, model.ChatHomeserverUnset).Message).
			To(ContainSubstring("--chat-homeserver"))
	})

	It("never takes over an alias another account holds", func() {
		ns := newProjectNamespace()
		chatBot(ns)
		mallory, _ := chatFake.NewUser("mallory-" + ns)
		chatFake.CreateRoomAs(mallory, "theos-"+ns+"-triage")
		chat := newChat(ns, "chat", "matrix-bot")
		mustCreate(chat)
		cond := chatReady(chat, metav1.ConditionFalse, model.ChatAliasTaken)
		Expect(cond.Message).To(ContainSubstring("Alias #theos-" + ns + "-triage:" + matrixtest.ServerName))
		Expect(cond.Message).To(ContainSubstring("spec.aliasPrefix"))
		// What it made before the taken alias is kept in status.
		Expect(chat.Status.Space).NotTo(BeNil())
		Expect(chat.Status.Rooms).To(HaveLen(2))

		By("taking a prefix of its own on a new Chat")
		Expect(k8sClient.Delete(ctx, chat)).To(Succeed())
		own := newChat(ns, "own", "matrix-bot")
		own.Spec.AliasPrefix = "acme-" + ns
		mustCreate(own)
		chatReady(own, metav1.ConditionTrue, model.ChatReady)
		Expect(own.Status.Space.Alias).To(Equal("#acme-" + ns + ":" + matrixtest.ServerName))
	})

	It("leaves a second Chat in the namespace to the first", func() {
		ns := newProjectNamespace()
		chatBot(ns)
		first := newChat(ns, "first", "matrix-bot")
		mustCreate(first)
		chatReady(first, metav1.ConditionTrue, model.ChatReady)
		second := newChat(ns, "second", "matrix-bot")
		mustCreate(second)
		Expect(chatReady(second, metav1.ConditionFalse, model.ChatDuplicate).Message).
			To(ContainSubstring("Chat first already serves this namespace"))
		Expect(second.Status.Rooms).To(BeEmpty())
	})

	DescribeTable("refuses a Chat its CEL rules don't allow",
		func(mutate func(*c4v1beta1.Chat), message string) {
			ns := newProjectNamespace()
			chat := newChat(ns, "chat", "matrix-bot",
				c4v1beta1.ChatPerson{MatrixID: "@ada:example.org", User: "ada@example.com"})
			mutate(chat)
			Expect(k8sClient.Create(ctx, chat)).To(MatchError(ContainSubstring(message)))
		},
		Entry("a homeserver that isn't http or https", func(c *c4v1beta1.Chat) { c.Spec.Homeserver = "ftp://matrix.example" },
			"homeserver must be an http or https address"),
		Entry("a homeserver with a query", func(c *c4v1beta1.Chat) { c.Spec.Homeserver = "https://matrix.example/?a=b" },
			"homeserver must be an http or https address"),
		Entry("a matrixID that isn't one", func(c *c4v1beta1.Chat) { c.Spec.People[0].MatrixID = "ada" },
			"matrixID must be a Matrix user ID"),
		Entry("a person twice", func(c *c4v1beta1.Chat) { c.Spec.People = append(c.Spec.People, c.Spec.People[0]) },
			"Duplicate value"),
		Entry("a system: user", func(c *c4v1beta1.Chat) { c.Spec.People[0].User = "system:admin" },
			"user must be a person's name, not a system: identity"),
		Entry("a system: group", func(c *c4v1beta1.Chat) { c.Spec.People[0].Groups = []string{"team", "system:masters"} },
			"groups must be people's groups, not system: groups"),
		Entry("two rooms of one name", func(c *c4v1beta1.Chat) { c.Spec.Rooms.General = "alerts" },
			"each room needs a name of its own"),
		Entry("a digest over a day", func(c *c4v1beta1.Chat) { c.Spec.DigestMinutes = 1441 },
			"spec.digestMinutes"),
		Entry("an alias prefix Matrix can't hold", func(c *c4v1beta1.Chat) { c.Spec.AliasPrefix = "Shop:x" },
			"spec.aliasPrefix"),
		Entry("no Secret", func(c *c4v1beta1.Chat) { c.Spec.CredentialsSecretRef.Name = "" },
			"spec.credentialsSecretRef.name"),
	)

	It("keeps its alias prefix as it was made", func() {
		ns := newProjectNamespace()
		unset := newChat(ns, "unset", "matrix-bot")
		mustCreate(unset)
		Expect(patch(unset, func() { unset.Spec.AliasPrefix = "acme" })).
			To(MatchError(ContainSubstring("aliasPrefix can't be set or unset once the Chat exists")))

		set := newChat(ns, "set", "matrix-bot")
		set.Spec.AliasPrefix = "acme"
		mustCreate(set)
		Expect(patch(set, func() { set.Spec.AliasPrefix = "other" })).
			To(MatchError(ContainSubstring("aliasPrefix is immutable")))
		Expect(patch(set, func() { set.Spec.Space = "Shop" })).To(Succeed())
	})
})
