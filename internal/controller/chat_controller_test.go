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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
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
			To(ContainSubstring("refused the bot's token in Secret stale"))

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

	It("reads a Chat that names no host as on matrix, and keeps its host as it was made (#158)", func() {
		ns := newProjectNamespace()
		matrix := newChat(ns, "matrix", "matrix-bot")
		mustCreate(matrix)
		Expect(k8sClient.Get(ctx, key(ns, "matrix"), matrix)).To(Succeed())
		Expect(matrix.Spec.Host).To(Equal(c4v1beta1.ChatHostMatrix))
		Expect(patch(matrix, func() { matrix.Spec.Host, matrix.Spec.Homeserver = c4v1beta1.ChatHostSlack, "" })).
			To(MatchError(ContainSubstring("host is immutable")))

		slack := newSlackChat(ns, "slack")
		mustCreate(slack)
		Expect(patch(slack, func() { slack.Spec.Host = c4v1beta1.ChatHostMatrix })).
			To(MatchError(ContainSubstring("host is immutable")))
		Expect(k8sClient.Get(ctx, key(ns, "slack"), slack)).To(Succeed())
		Expect(patch(slack, func() { slack.Spec.Host = "" })).
			To(MatchError(ContainSubstring("host is immutable")), "unset, host is matrix again")
		Expect(k8sClient.Get(ctx, key(ns, "slack"), slack)).To(Succeed())
		Expect(patch(slack, func() { slack.Spec.DigestMinutes = 5 })).To(Succeed())
	})

	DescribeTable("refuses a Slack Chat its CEL rules don't allow (#158)",
		func(mutate func(*c4v1beta1.Chat), message string) {
			ns := newProjectNamespace()
			chat := newSlackChat(ns, "chat")
			chat.Spec.Slack = &c4v1beta1.ChatSlack{People: []c4v1beta1.ChatSlackPerson{
				{SlackID: "U012AB3CD", User: "ada@example.com"}}}
			mutate(chat)
			Expect(k8sClient.Create(ctx, chat)).To(MatchError(ContainSubstring(message)))
		},
		Entry("slack on a Chat that names no host", func(c *c4v1beta1.Chat) { c.Spec.Host = "" },
			"slack is only for a Chat with host: slack"),
		Entry("slack on a Matrix Chat", func(c *c4v1beta1.Chat) {
			c.Spec.Host, c.Spec.Homeserver = c4v1beta1.ChatHostMatrix, chatFake.URL()
		}, "slack is only for a Chat with host: slack"),
		Entry("a host the factory doesn't know", func(c *c4v1beta1.Chat) { c.Spec.Host = "teams" },
			"spec.host: Unsupported value"),
		Entry("a homeserver", func(c *c4v1beta1.Chat) { c.Spec.Homeserver = "https://matrix.example" },
			"a Chat with host: slack names no homeserver"),
		Entry("a space", func(c *c4v1beta1.Chat) { c.Spec.Space = "Shop" },
			"a Chat with host: slack names no space"),
		Entry("Matrix people", func(c *c4v1beta1.Chat) {
			c.Spec.People = []c4v1beta1.ChatPerson{{MatrixID: "@ada:example.org", User: "ada@example.com"}}
		}, "a Chat with host: slack lists its people under slack.people"),
		Entry("a renamed room", func(c *c4v1beta1.Chat) { c.Spec.Rooms.General = "lobby" },
			"a Chat with host: slack keeps each room's name its purpose"),
		Entry("an alias prefix with a dot", func(c *c4v1beta1.Chat) { c.Spec.AliasPrefix = "acme.shop" },
			"a Chat with host: slack needs an aliasPrefix Slack takes in a channel name"),
		Entry("an alias prefix of 71 characters", func(c *c4v1beta1.Chat) { c.Spec.AliasPrefix = strings.Repeat("a", 71) },
			"a Chat with host: slack needs an aliasPrefix Slack takes in a channel name"),
		Entry("a slackID that isn't a member ID", func(c *c4v1beta1.Chat) { c.Spec.Slack.People[0].SlackID = "ada" },
			"slackID must be a Slack member ID"),
		Entry("a bot's ID", func(c *c4v1beta1.Chat) { c.Spec.Slack.People[0].SlackID = "B012AB3CD" },
			"slackID must be a Slack member ID"),
		Entry("a Slack person twice", func(c *c4v1beta1.Chat) {
			c.Spec.Slack.People = append(c.Spec.Slack.People, c.Spec.Slack.People[0])
		}, "Duplicate value"),
		Entry("a system: user", func(c *c4v1beta1.Chat) { c.Spec.Slack.People[0].User = "system:admin" },
			"user must be a person's name, not a system: identity"),
		Entry("a system: group", func(c *c4v1beta1.Chat) {
			c.Spec.Slack.People[0].Groups = []string{"team", "system:masters"}
		}, "groups must be people's groups, not system: groups"),
	)

	It("takes a Slack Chat with its rooms at their defaults and a prefix Slack takes (#158)", func() {
		ns := newProjectNamespace()
		chat := newSlackChat(ns, "chat")
		chat.Spec.Rooms.General, chat.Spec.AliasPrefix = "general", "acme-shop-"+strings.Repeat("a", 60)
		chat.Spec.Slack = &c4v1beta1.ChatSlack{}
		mustCreate(chat)
	})

	It("makes a Slack Chat's six private channels and no Space, and invites its people (#159)", func() {
		ns := newProjectNamespace()
		ada, _ := chatSlackFake.NewUser("ada-" + ns)
		adaUser, bobUser := "ada-"+ns+"@example.com", "bob-"+ns+"@example.com"
		grant(adaUser, roleChatPeople)
		grant(bobUser, roleChatPeople)
		chat, bot, _ := slackFakeChat(ns, c4v1beta1.ChatSlackPerson{SlackID: ada, User: adaUser})
		Expect(asUser(adaUser).Create(ctx, chat)).To(Succeed())

		cond := chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(cond.Message).To(Equal("The 6 rooms are ready on " + chatSlackFake.Address() +
			", with no Space; 1 person invited"))
		Expect(chat.Status.Homeserver).To(Equal(chatSlackFake.Address()), "the address theos-chat reaches Slack at")
		Expect(chat.Status.Bot).To(Equal(bot))
		Expect(chat.Status.Space).To(BeNil(), "Slack has no Space")
		Expect(chat.Status.Invited).To(Equal([]string{ada}))
		Expect(chat.Status.Rooms).To(HaveLen(6))
		ids := make([]string, 0, len(model.ChatPurposes))
		for i, purpose := range model.ChatPurposes {
			status := chat.Status.Rooms[i]
			name := "theos-" + ns + "-" + string(purpose)
			Expect(status.Purpose).To(Equal(c4v1beta1.ChatPurpose(purpose)))
			Expect(status.Alias).To(Equal(name))
			channel, ok := chatSlackFake.Room(status.ID)
			Expect(ok).To(BeTrue())
			Expect(channel).To(And(HaveField("Name", name), HaveField("Topic", model.ChatTopic(purpose)),
				HaveField("Space", false), HaveField("Parent", ""), HaveField("Private", true)))
			Expect(channel.Members).To(ConsistOf(bot, ada))
			ids = append(ids, status.ID)
		}

		By("making nothing on the next reconciles, and inviting a person added")
		made := chatSlackFake.Rooms()
		bob, _ := chatSlackFake.NewUser("bob-" + ns)
		Expect(patchAs(asUser(bobUser), chat, func() {
			chat.Spec.Slack.People = append(chat.Spec.Slack.People, c4v1beta1.ChatSlackPerson{SlackID: bob, User: bobUser})
		})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "chat"), chat)).To(Succeed())
			g.Expect(chat.Status.Invited).To(ConsistOf(ada, bob))
			g.Expect(condition(chat.Status.Conditions, c4v1beta1.ConditionChatReady).ObservedGeneration).
				To(Equal(chat.Generation))
		}).Should(Succeed())
		for _, id := range ids {
			channel, _ := chatSlackFake.Room(id)
			Expect(channel.Members).To(ConsistOf(bot, ada, bob))
		}
		Expect(chatSlackFake.Rooms()).To(Equal(made))
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
	})

	It("names a Slack Chat's missing key and a scope its bot token lacks on Ready (#159)", func() {
		ns := newProjectNamespace()
		_, token := chatSlackFake.NewUser("bot-" + ns)
		secret := &corev1.Secret{ObjectMeta: objectMeta(ns, "slack-bot"),
			Data: map[string][]byte{model.ChatSlackBotTokenKey: []byte(token)}}
		mustCreate(secret)
		chat := newSlackChat(ns, "chat")
		mustCreate(chat)
		Expect(chatReady(chat, metav1.ConditionFalse, model.ChatSecretIncomplete).Message).To(HavePrefix(
			"Secret slack-bot has no appToken; it holds the bot token under botToken and the app-level token under " +
				"appToken"))
		Expect(chat.Status.Rooms).To(BeEmpty(), "nothing is made before the Secret is whole")

		By("saying the scope Slack names, and making the channels once the bot has it")
		chatSlackFake.Revoke(token, "groups:write", "EnsureRoom")
		Expect(patch(secret, func() { secret.Data[model.ChatSlackAppTokenKey] = []byte(rand.Text()) })).To(Succeed())
		Expect(chatReady(chat, metav1.ConditionFalse, model.ChatScopeMissing).Message).To(HavePrefix(
			"The bot's token in Secret slack-bot lacks the scope groups:write, which " + chatSlackFake.Address() +
				" says a call needs"))
		chatSlackFake.Grant(token, "groups:write")
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(chat.Status.Rooms).To(HaveLen(6))

		By("naming the bot token when it is the key missing")
		ns = newProjectNamespace()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "slack-bot"),
			Data: map[string][]byte{model.ChatSlackAppTokenKey: []byte(rand.Text())}})
		other := newSlackChat(ns, "chat")
		mustCreate(other)
		Expect(chatReady(other, metav1.ConditionFalse, model.ChatSecretIncomplete).Message).To(HavePrefix(
			"Secret slack-bot has no botToken"))
	})

	It("invites the others when the homeserver refuses one person, stays Ready and names the refusal, and "+
		"invites them once their account exists (#251)", func() {
		ns := newProjectNamespace()
		chatBot(ns)
		ada, _ := chatFake.NewUser("ada-" + ns)
		carol, _ := chatFake.NewUser("carol-" + ns)
		// ghost has no account on the homeserver yet: it answers 404 M_NOT_FOUND.
		ghost := "@ghost-" + ns + ":" + matrixtest.ServerName
		people := []c4v1beta1.ChatPerson{{MatrixID: ada, User: "ada-" + ns + "@example.com"},
			{MatrixID: ghost, User: "ghost-" + ns + "@example.com"}, {MatrixID: carol, User: "carol-" + ns + "@example.com"}}
		chat := newChat(ns, "chat", "matrix-bot", people[0])
		createByItsPeople(chat, people[0].User,
			func(i int) string { chat.Spec.People = append(chat.Spec.People, people[i]); return people[i].User },
			len(people))

		cond := chatInvited(chat, metav1.ConditionFalse, model.ChatInviteRefused)
		Expect(cond.Message).To(ContainSubstring(ghost + " (M_NOT_FOUND)"))
		Expect(cond.Message).To(ContainSubstring("refused to invite"))
		ready := chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(ready.Reason).NotTo(Equal(model.ChatUnreachable))
		Expect(ready.Message).To(HaveSuffix("; 2 people invited"))
		Expect(chat.Status.Invited).To(ConsistOf(ada, carol))
		Expect(chat.Status.Rooms).To(HaveLen(6))
		for _, id := range chatRoomIDs(chat) {
			room, ok := chatFake.Room(id)
			Expect(ok).To(BeTrue())
			Expect(room.Members).To(And(HaveKeyWithValue(ada, "invite"), HaveKeyWithValue(carol, "invite"),
				Not(HaveKey(ghost))), "room %s", id)
		}

		By("inviting ghost once the account exists, on the next poll")
		chatFake.NewUser("ghost-" + ns)
		Expect(chatInvited(chat, metav1.ConditionTrue, model.ChatInvited).Message).
			To(Equal("The 3 people in spec.people are invited to every room"))
		Expect(chat.Status.Invited).To(ConsistOf(ada, ghost, carol))
		for _, id := range chatRoomIDs(chat) {
			room, _ := chatFake.Room(id)
			Expect(room.Members).To(HaveKeyWithValue(ghost, "invite"), "room %s", id)
		}
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
	})

	It("invites the others when Slack refuses one person, stays Ready and names the refusal, and invites them "+
		"once the member is active again (#251)", func() {
		ns := newProjectNamespace()
		ada, _ := chatSlackFake.NewUser("ada-" + ns)
		ghost, _ := chatSlackFake.NewUser("ghost-" + ns)
		carol, _ := chatSlackFake.NewUser("carol-" + ns)
		// ghost is deactivated: Slack answers user_not_found.
		chatSlackFake.Deactivate(ghost)
		people := []c4v1beta1.ChatSlackPerson{{SlackID: ada, User: "ada-" + ns + "@example.com"},
			{SlackID: ghost, User: "ghost-" + ns + "@example.com"}, {SlackID: carol, User: "carol-" + ns + "@example.com"}}
		chat, bot, _ := slackFakeChat(ns, people[0])
		createByItsPeople(chat, people[0].User,
			func(i int) string {
				chat.Spec.Slack.People = append(chat.Spec.Slack.People, people[i])
				return people[i].User
			}, len(people))

		cond := chatInvited(chat, metav1.ConditionFalse, model.ChatInviteRefused)
		Expect(cond.Message).To(HavePrefix(chatSlackFake.Address() + " refused to invite " + ghost + " (" +
			fake.CodeUserNotFound + "); the other 2 people in spec.slack.people are invited"))
		ready := chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(ready.Reason).NotTo(Equal(model.ChatUnreachable))
		Expect(chat.Status.Invited).To(ConsistOf(ada, carol))
		Expect(chat.Status.Rooms).To(HaveLen(6))
		for _, room := range chat.Status.Rooms {
			channel, ok := chatSlackFake.Room(room.ID)
			Expect(ok).To(BeTrue())
			Expect(channel.Members).To(ConsistOf(bot, ada, carol), "channel %s", room.Purpose)
		}

		By("inviting ghost once the member is active again, on the next poll")
		chatSlackFake.Reactivate(ghost)
		chatInvited(chat, metav1.ConditionTrue, model.ChatInvited)
		Expect(chat.Status.Invited).To(ConsistOf(ada, ghost, carol))
		for _, room := range chat.Status.Rooms {
			channel, _ := chatSlackFake.Room(room.ID)
			Expect(channel.Members).To(ConsistOf(bot, ada, ghost, carol), "channel %s", room.Purpose)
		}
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
	})

	It("invites the others when the rooms have banned one person, stays Ready and names the ban's code (#251)",
		func() {
			ns := newProjectNamespace()
			chatBot(ns)
			ada, _ := chatFake.NewUser("ada-" + ns)
			banned, _ := chatFake.NewUser("banned-" + ns)
			carol, _ := chatFake.NewUser("carol-" + ns)
			// Every room bans banned, those the operator makes too: an invite
			// is 403 M_FORBIDDEN and the room's state says ban.
			chatFake.Ban(banned)
			people := []c4v1beta1.ChatPerson{{MatrixID: ada, User: "ada-" + ns + "@example.com"},
				{MatrixID: banned, User: "banned-" + ns + "@example.com"},
				{MatrixID: carol, User: "carol-" + ns + "@example.com"}}
			chat := newChat(ns, "chat", "matrix-bot", people[0])
			createByItsPeople(chat, people[0].User,
				func(i int) string { chat.Spec.People = append(chat.Spec.People, people[i]); return people[i].User },
				len(people))

			cond := chatInvited(chat, metav1.ConditionFalse, model.ChatInviteRefused)
			Expect(cond.Message).To(ContainSubstring("refused to invite " + banned + " (M_FORBIDDEN)"))
			ready := chatReady(chat, metav1.ConditionTrue, model.ChatReady)
			Expect(ready.Reason).NotTo(Equal(model.ChatUnreachable))
			Expect(chat.Status.Invited).To(ConsistOf(ada, carol))
			Expect(chat.Status.Rooms).To(HaveLen(6))
			Expect(chat.Status.Refused).To(HaveLen(1))
			Expect(chat.Status.Refused[0].ID).To(Equal(banned))
			Expect(chat.Status.Refused[0].Rooms).To(ConsistOf(chatRoomIDs(chat)))
			for _, id := range chatRoomIDs(chat) {
				room, ok := chatFake.Room(id)
				Expect(ok).To(BeTrue())
				Expect(room.Members).To(And(HaveKeyWithValue(ada, "invite"), HaveKeyWithValue(carol, "invite"),
					Not(HaveKey(banned))), "room %s", id)
			}
		})

	It("asks a person Slack refused in one channel to that channel alone, and never pulls them back into a "+
		"channel they left (#251)", func() {
		ns := newProjectNamespace()
		ada, _ := chatSlackFake.NewUser("ada-" + ns)
		bob, _ := chatSlackFake.NewUser("bob-" + ns)
		adaPerson := c4v1beta1.ChatSlackPerson{SlackID: ada, User: "ada-" + ns + "@example.com"}
		bobPerson := c4v1beta1.ChatSlackPerson{SlackID: bob, User: "bob-" + ns + "@example.com"}
		chat, bot, _ := slackFakeChat(ns, adaPerson)
		createByItsPeople(chat, adaPerson.User, nil, 1)
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		Expect(chat.Status.Rooms).To(HaveLen(6))
		byPurpose := map[c4v1beta1.ChatPurpose]string{}
		for _, room := range chat.Status.Rooms {
			byPurpose[room.Purpose] = room.ID
		}
		alerts, general := byPurpose[c4v1beta1.ChatPurpose(model.ChatAlerts)],
			byPurpose[c4v1beta1.ChatPurpose(model.ChatGeneral)]

		By("adding bob, a single-channel guest Slack refuses in alerts")
		chatSlackFake.RefuseIn(alerts, bob, "ura_max_channels")
		grant(bobPerson.User, roleChatPeople)
		orig := chat.DeepCopy()
		chat.Spec.Slack.People = append(chat.Spec.Slack.People, bobPerson)
		Expect(asUser(bobPerson.User).Patch(ctx, chat, client.MergeFrom(orig))).To(Succeed())
		cond := chatInvited(chat, metav1.ConditionFalse, model.ChatInviteRefused)
		Expect(cond.Message).To(ContainSubstring(bob + " (ura_max_channels)"))
		Expect(chat.Status.Invited).To(ConsistOf(ada))
		Expect(chat.Status.Refused).To(ConsistOf(c4v1beta1.ChatRefusedPerson{ID: bob, Code: "ura_max_channels",
			Rooms: []string{alerts}}))
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		for _, room := range chat.Status.Rooms {
			channel, _ := chatSlackFake.Room(room.ID)
			if room.ID == alerts {
				Expect(channel.Members).To(ConsistOf(bot, ada), "channel %s", room.Purpose)
			} else {
				Expect(channel.Members).To(ConsistOf(bot, ada, bob), "channel %s", room.Purpose)
			}
		}

		By("bob leaving general: the passes that ask him to alerts again leave general alone")
		Expect(chatSlackFake.Remove(general, bob)).To(Succeed())
		Consistently(func(g Gomega) {
			channel, _ := chatSlackFake.Room(general)
			g.Expect(channel.Members).NotTo(ContainElement(bob))
		}, "4s", "200ms").Should(Succeed())

		By("lifting the refusal: bob is in alerts on the next pass, and still out of general")
		chatSlackFake.RefuseIn(alerts, bob, "")
		chatInvited(chat, metav1.ConditionTrue, model.ChatInvited)
		Expect(chat.Status.Invited).To(ConsistOf(ada, bob))
		Expect(chat.Status.Refused).To(BeEmpty())
		channel, _ := chatSlackFake.Room(alerts)
		Expect(channel.Members).To(ConsistOf(bot, ada, bob))
		channel, _ = chatSlackFake.Room(general)
		Expect(channel.Members).To(ConsistOf(bot, ada))
	})
})

// createByItsPeople creates chat, which lists its first person alone, as
// that person, first, then adds each further person as themselves
// (config/policy/chat_people_policy.yaml): add(i) appends person i and
// returns their user. n counts the people.
func createByItsPeople(chat *c4v1beta1.Chat, first string, add func(i int) string, n int) {
	GinkgoHelper()
	grant(first, roleChatPeople)
	Expect(asUser(first).Create(ctx, chat)).To(Succeed())
	for i := 1; i < n; i++ {
		orig := chat.DeepCopy()
		user := add(i)
		grant(user, roleChatPeople)
		Expect(asUser(user).Patch(ctx, chat, client.MergeFrom(orig))).To(Succeed())
	}
}

// chatInvited waits for chat's Invited to have status and reason, at the
// Chat's generation, and returns it (#251).
func chatInvited(chat *c4v1beta1.Chat, status metav1.ConditionStatus, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(chat.Namespace, chat.Name), chat)).To(Succeed())
		cond = condition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.ObservedGeneration).To(Equal(chat.Generation))
	}).Should(Succeed())
	return cond
}

// newSlackChat is a Chat with host: slack whose bot's tokens are in Secret
// slack-bot.
func newSlackChat(ns, name string) *c4v1beta1.Chat {
	return &c4v1beta1.Chat{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.ChatSpec{
		Host: c4v1beta1.ChatHostSlack, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "slack-bot"}}}
}
