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

package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Each event kind goes to the room the plan's table names, as a digest, a
// live item (a thread) or a plain message.
func TestChatRouteFor(t *testing.T) {
	tests := []struct {
		kind ChatEventKind
		want ChatRoute
	}{
		{ChatEventMerged, ChatRoute{Room: ChatUpdates, Digest: true}},
		{ChatEventReleased, ChatRoute{Room: ChatUpdates, Digest: true}},
		{ChatEventDeployed, ChatRoute{Room: ChatUpdates, Digest: true}},
		{ChatEventBriefRound, ChatRoute{Room: ChatUpdates, Digest: true}},
		{ChatEventAnswer, ChatRoute{Room: ChatGeneral}},
		{ChatEventTriaged, ChatRoute{Room: ChatTriage}},
		{ChatEventTriageQuestion, ChatRoute{Room: ChatTriage, Thread: true}},
		{ChatEventSpecWaiting, ChatRoute{Room: ChatSpecs, Thread: true}},
		{ChatEventDeployWaiting, ChatRoute{Room: ChatReviews, Thread: true}},
		{ChatEventReviewVerdict, ChatRoute{Room: ChatReviews}},
		{ChatEventChangeWaiting, ChatRoute{Room: ChatReviews, Thread: true}},
		{ChatEventDeployFailed, ChatRoute{Room: ChatAlerts}},
		{ChatEventTaskFailed, ChatRoute{Room: ChatAlerts}},
		{ChatEventContainerFailed, ChatRoute{Room: ChatAlerts, Thread: true}},
		{ChatEventBaseRed, ChatRoute{Room: ChatAlerts}},
		{ChatEventUnhealthy, ChatRoute{Room: ChatAlerts}},
	}
	rooms := map[ChatPurpose]bool{}
	for _, tt := range tests {
		got, err := ChatRouteFor(tt.kind)
		if err != nil || got != tt.want {
			t.Errorf("ChatRouteFor(%s) = %+v, %v; want %+v", tt.kind, got, err, tt.want)
		}
		rooms[got.Room] = true
	}
	if len(tests) != len(chatRoutes) {
		t.Errorf("the table tests %d event kinds; chatRoutes has %d", len(tests), len(chatRoutes))
	}
	for _, p := range ChatPurposes {
		if !rooms[p] {
			t.Errorf("no event goes to %s", p)
		}
		if ChatTopic(p) == "" {
			t.Errorf("room %s has no topic", p)
		}
	}
	if _, err := ChatRouteFor("Gossip"); err == nil {
		t.Error("ChatRouteFor(Gossip) found a room")
	}
}

func TestChatNames(t *testing.T) {
	if got := ChatAliasPrefix("webshop", ""); got != "theos-webshop" {
		t.Errorf("ChatAliasPrefix = %q", got)
	}
	if got := ChatAliasPrefix("webshop", "acme-shop"); got != "acme-shop" {
		t.Errorf("ChatAliasPrefix with a prefix = %q", got)
	}
	if got := ChatRoomAlias("theos-webshop", ChatSpecs); got != "theos-webshop-specs" {
		t.Errorf("ChatRoomAlias = %q", got)
	}
	for _, tt := range []struct{ space, project, want string }{
		{"Shop", "Web shop", "Shop"},
		{"", "Web shop", "Web shop"},
		{" ", "", "webshop"},
	} {
		if got := ChatSpaceName(tt.space, tt.project, "webshop"); got != tt.want {
			t.Errorf("ChatSpaceName(%q, %q) = %q, want %q", tt.space, tt.project, got, tt.want)
		}
	}
}

func TestChatReadyMessage(t *testing.T) {
	c := ChatCheck{Chat: "chat", Secret: "matrix-bot", Homeserver: "https://matrix.example", Space: "#theos-shop:example",
		Alias: "#theos-shop-general:example", Rooms: 6, People: 1, Other: "first", Detail: "M_UNKNOWN_TOKEN"}
	for reason, want := range map[string]string{
		ChatReady:             "Space #theos-shop:example and its 6 rooms are ready on https://matrix.example; 1 person invited",
		ChatSecretMissing:     "Secret matrix-bot is missing; whoever owns Chat chat creates it with the bot's access token under accessToken",
		ChatSecretIncomplete:  "Secret matrix-bot has no accessToken",
		ChatHomeserverUnset:   "Chat chat names no homeserver and the operator has none (--chat-homeserver)",
		ChatCredentialRefused: "https://matrix.example refused the access token in Secret matrix-bot: M_UNKNOWN_TOKEN",
		ChatUnreachable:       "Could not reach the rooms on https://matrix.example: M_UNKNOWN_TOKEN",
		ChatAliasTaken:        "Alias #theos-shop-general:example on https://matrix.example belongs to a room the bot is not in",
		ChatDuplicate:         "Chat first already serves this namespace",
	} {
		if got := ChatReadyMessage(reason, c); !strings.HasPrefix(got, want) {
			t.Errorf("ChatReadyMessage(%s) = %q, want it to start with %q", reason, got, want)
		}
	}
}

// A post's every field is shown as written: HTML in a title, a question or
// a code line is escaped, and only an http or https link is a link.
func TestChatPostHTML(t *testing.T) {
	tests := []struct {
		name string
		post ChatPost
		want string
	}{
		{"title and status", ChatPost{Title: "Spec: dark mode", Status: "Waiting for approval"},
			"<p><b>Spec: dark mode</b><br>Waiting for approval</p>"},
		{"status alone", ChatPost{Status: "Merged"}, "<p>Merged</p>"},
		{"escapes the title and keeps line breaks",
			ChatPost{Title: "<script>alert(1)</script>", Body: []string{"a & b\n\"c\" 'd' <i>e</i>"}},
			"<p><b>&lt;script&gt;alert(1)&lt;/script&gt;</b></p>" +
				"<p>a &amp; b<br>&#34;c&#34; &#39;d&#39; &lt;i&gt;e&lt;/i&gt;</p>"},
		{"links, the address escaped",
			ChatPost{Links: []ChatLink{{"Pull request #4", "https://git.example/acme/shop/pulls/4?a=1&b=\"2\""},
				{"", "http://ui.example/?ns=shop"}}},
			`<p><a href="https://git.example/acme/shop/pulls/4?a=1&amp;b=&#34;2&#34;">Pull request #4</a> · ` +
				`<a href="http://ui.example/?ns=shop">http://ui.example/?ns=shop</a></p>`},
		{"drops a link that isn't http or https",
			ChatPost{Links: []ChatLink{{"click", "javascript:alert(1)"}, {"data", "data:text/html,<b>x</b>"},
				{"relative", "/k8s"}, {"spaced", "https://a.example/x y"}, {"userinfo", "https://u:p@a.example/"}}},
			""},
		{"a code block, escaped",
			ChatPost{Code: []string{"kubectl -n shop annotate container api theos.factory.io/retry=$(date +%s) --overwrite",
				"echo '<b>' && true"}},
			"<pre><code>kubectl -n shop annotate container api theos.factory.io/retry=$(date +%s) --overwrite\n" +
				"echo &#39;&lt;b&gt;&#39; &amp;&amp; true</code></pre>"},
		{"blank fields leave nothing", ChatPost{Body: []string{" ", ""}, Code: []string{""}}, ""},
	}
	for _, tt := range tests {
		if got := tt.post.HTML(); got != tt.want {
			t.Errorf("%s: HTML() =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

// TestChatPostMrkdwn covers #155: a post for Slack is mrkdwn with &, < and >
// escaped in every field, so text that looks like a link, a mention or an
// entity is shown as written, and only an http or https link is a link.
func TestChatPostMrkdwn(t *testing.T) {
	tests := []struct {
		name string
		post ChatPost
		want string
	}{
		{"title and status", ChatPost{Title: "Spec: dark mode", Status: "Waiting for approval"},
			"*Spec: dark mode*\nWaiting for approval"},
		{"status alone", ChatPost{Status: "Merged"}, "Merged"},
		{"a title on one line, bold", ChatPost{Title: " Spec:\ndark  mode "}, "*Spec: dark mode*"},
		{"escapes the three characters and keeps line breaks",
			ChatPost{Title: "a < b", Body: []string{"<@U012AB3CD> & <!here>\n<https://evil.example|x> &amp; > y"}},
			"*a &lt; b*\n\n&lt;@U012AB3CD&gt; &amp; &lt;!here&gt;\n&lt;https://evil.example|x&gt; &amp;amp; &gt; y"},
		{"paragraphs apart", ChatPost{Body: []string{"one", " ", "two"}}, "one\n\ntwo"},
		{"links, the address escaped and a | in it encoded",
			ChatPost{Links: []ChatLink{{"Pull request #4", "https://git.example/acme/shop/pulls/4?a=1&b=2|3"},
				{"", "http://ui.example/?ns=shop"}, {"a > b\nc", "https://ui.example/"}}},
			"<https://git.example/acme/shop/pulls/4?a=1&amp;b=2%7C3|Pull request #4> · " +
				"<http://ui.example/?ns=shop|http://ui.example/?ns=shop> · <https://ui.example/|a &gt; b c>"},
		{"drops a link that isn't http or https",
			ChatPost{Links: []ChatLink{{"click", "javascript:alert(1)"}, {"relative", "/k8s"},
				{"spaced", "https://a.example/x y"}, {"userinfo", "https://u:p@a.example/"}}},
			""},
		{"a code block, escaped",
			ChatPost{Code: []string{"kubectl get containers", "echo '<b>' && true"}},
			"```\nkubectl get containers\necho '&lt;b&gt;' &amp;&amp; true\n```"},
		{"every part, in order",
			ChatPost{Title: "Deploy", Body: []string{"why"}, Links: []ChatLink{{"UI", "https://ui.example/"}},
				Code: []string{"kubectl"}},
			"*Deploy*\n\nwhy\n\n<https://ui.example/|UI>\n\n```\nkubectl\n```"},
		{"blank fields leave nothing", ChatPost{Body: []string{" ", ""}, Code: []string{""}}, ""},
	}
	for _, tt := range tests {
		if got := tt.post.Mrkdwn(); got != tt.want {
			t.Errorf("%s: Mrkdwn() =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestChatPostText(t *testing.T) {
	post := ChatPost{Title: "Deploy waiting", Status: "api v1.2.0 to production",
		Body:  []string{"<b>not markup</b>"},
		Links: []ChatLink{{"Open in the UI", "https://ui.example/?kind=Container"}, {"bad", "javascript:x"}},
		Code:  []string{"kubectl get containers"}}
	want := "Deploy waiting\napi v1.2.0 to production\n\n<b>not markup</b>\n\n" +
		"Open in the UI: https://ui.example/?kind=Container\n\nkubectl get containers"
	if got := post.Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
	if got := ChatHelp(ChatSpecs).Text(); !strings.Contains(got, "approve, or changes: <text>") {
		t.Errorf("ChatHelp(specs) = %q", got)
	}
}

func TestParseChatCommand(t *testing.T) {
	tests := []struct {
		room     ChatPurpose
		inThread bool
		body     string
		want     ChatCommand
		err      error
	}{
		// general
		{ChatGeneral, false, "status", ChatCommand{Kind: ChatCommandStatus}, nil},
		{ChatGeneral, false, "  Status  ", ChatCommand{Kind: ChatCommandStatus}, nil},
		{ChatGeneral, false, "help", ChatCommand{Kind: ChatCommandHelp}, nil},
		{ChatGeneral, false, "brief a shop for plants, with delivery",
			ChatCommand{Kind: ChatCommandBrief, Text: "a shop for plants, with delivery"}, nil},
		{ChatGeneral, false, "brief: a shop", ChatCommand{Kind: ChatCommandBrief, Text: "a shop"}, nil},
		{ChatGeneral, false, "prompt shop: add a dark mode",
			ChatCommand{Kind: ChatCommandPrompt, Brief: "shop", Text: "add a dark mode"}, nil},
		{ChatGeneral, false, "prompt shop: keep: the colons", ChatCommand{Kind: ChatCommandPrompt, Brief: "shop",
			Text: "keep: the colons"}, nil},
		{ChatGeneral, false, "pause shop", ChatCommand{Kind: ChatCommandPause, Brief: "shop"}, nil},
		{ChatGeneral, true, "resume web.shop-2", ChatCommand{Kind: ChatCommandResume, Brief: "web.shop-2"}, nil},
		// A reply's quoted fallback is no part of the command.
		{ChatGeneral, false, "> <@ada:example.org> how is it going?\n> second line\n\nstatus",
			ChatCommand{Kind: ChatCommandStatus}, nil},
		{ChatGeneral, false, "status please", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "help me", ChatCommand{}, ErrChatUnknownCommand},
		{ChatGeneral, false, "brief", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "brief " + strings.Repeat("x", 4001), ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "prompt shop add a dark mode", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "prompt shop:", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "prompt ../secrets: x", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "prompt Shop: x", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "pause", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "pause shop now", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "pause shop/../x", ChatCommand{}, ErrChatInvalidCommand},
		{ChatGeneral, false, "delete shop", ChatCommand{}, ErrChatUnknownCommand},
		{ChatGeneral, false, "retry", ChatCommand{}, ErrChatUnknownCommand},
		{ChatGeneral, false, "status!", ChatCommand{}, ErrChatUnknownCommand},
		{ChatGeneral, false, "hello everyone", ChatCommand{}, ErrChatUnknownCommand},
		{ChatGeneral, false, "", ChatCommand{}, ErrChatUnknownCommand},
		// triage
		{ChatTriage, true, "It is for logged-in users only.",
			ChatCommand{Kind: ChatCommandAnswer, Text: "It is for logged-in users only."}, nil},
		{ChatTriage, true, "help", ChatCommand{Kind: ChatCommandHelp}, nil},
		{ChatTriage, false, "It is for logged-in users only.", ChatCommand{}, ErrChatInvalidCommand},
		{ChatTriage, true, "  ", ChatCommand{}, ErrChatInvalidCommand},
		// specs
		{ChatSpecs, true, "approve", ChatCommand{Kind: ChatCommandApproveSpec}, nil},
		{ChatSpecs, true, "Approve", ChatCommand{Kind: ChatCommandApproveSpec}, nil},
		{ChatSpecs, true, "changes: name the colour tokens", ChatCommand{Kind: ChatCommandChanges,
			Text: "name the colour tokens"}, nil},
		{ChatSpecs, true, "changes name them", ChatCommand{Kind: ChatCommandChanges, Text: "name them"}, nil},
		{ChatSpecs, false, "approve", ChatCommand{}, ErrChatInvalidCommand},
		{ChatSpecs, true, "approve v1", ChatCommand{}, ErrChatInvalidCommand},
		{ChatSpecs, true, "changes:", ChatCommand{}, ErrChatInvalidCommand},
		{ChatSpecs, true, "lgtm", ChatCommand{}, ErrChatUnknownCommand},
		// reviews
		{ChatReviews, true, "approve v1.2.0", ChatCommand{Kind: ChatCommandApproveDeploy, Version: "v1.2.0"}, nil},
		{ChatReviews, true, "approve api-v0.3.1+build.7", ChatCommand{Kind: ChatCommandApproveDeploy,
			Version: "api-v0.3.1+build.7"}, nil},
		// #92: a bare approve is a waiting change's; the thread's item says so,
		// and a deploy's thread refuses it at the write (ChatCommandRequest).
		{ChatReviews, true, "approve", ChatCommand{Kind: ChatCommandApproveSpec}, nil},
		{ChatReviews, true, "changes: keep the old column", ChatCommand{Kind: ChatCommandChanges,
			Text: "keep the old column"}, nil},
		{ChatReviews, true, "changes:", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, false, "approve", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, false, "changes: keep the old column", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, true, "approve v1 v2", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, true, "approve -v1", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, false, "approve v1.2.0", ChatCommand{}, ErrChatInvalidCommand},
		{ChatReviews, true, "ship it", ChatCommand{}, ErrChatUnknownCommand},
		// alerts
		{ChatAlerts, true, "retry", ChatCommand{Kind: ChatCommandRetry}, nil},
		{ChatAlerts, false, "retry", ChatCommand{}, ErrChatInvalidCommand},
		{ChatAlerts, true, "retry api", ChatCommand{}, ErrChatInvalidCommand},
		{ChatAlerts, true, "approve", ChatCommand{}, ErrChatUnknownCommand},
		// updates takes nothing but help.
		{ChatUpdates, false, "help", ChatCommand{Kind: ChatCommandHelp}, nil},
		{ChatUpdates, false, "status", ChatCommand{}, ErrChatInvalidCommand},
		{"lounge", false, "status", ChatCommand{}, ErrChatUnknownCommand},
	}
	for _, tt := range tests {
		got, err := ParseChatCommand(tt.room, tt.inThread, tt.body)
		if tt.err != nil {
			if !errors.Is(err, tt.err) {
				t.Errorf("ParseChatCommand(%s, %t, %q) = %+v, %v; want %v", tt.room, tt.inThread, tt.body, got, err,
					tt.err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseChatCommand(%s, %t, %q) = %+v, %v; want %+v", tt.room, tt.inThread, tt.body, got, err,
				tt.want)
		}
	}
}

// jsonEqual compares two JSON documents by value.
func jsonEqual(t *testing.T, got []byte, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("body %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

// Every command becomes exactly the one request the allowlist takes for it,
// and a command that writes nothing becomes none.
func TestChatCommandRequest(t *testing.T) {
	now := time.Unix(1762300000, 0)
	full := make([]string, 50)
	for i := range full {
		full[i] = "p" + string(rune('a'+i%26))
	}
	tests := []struct {
		name   string
		cmd    ChatCommand
		c      ChatContext
		method string
		res    string
		obj    string
		ctype  string
		body   string
		what   string
	}{
		{name: "brief", cmd: ChatCommand{Kind: ChatCommandBrief, Text: "a shop <for> \"plants\""},
			method: "POST", res: "briefs", ctype: ChatJSON, what: "create a Brief",
			body: `{"apiVersion":"theos.factory.io/v1beta1","kind":"Brief","metadata":{"generateName":"brief-"},` +
				`"spec":{"idea":"a shop <for> \"plants\""}}`},
		{name: "prompt to a Brief with prompts", cmd: ChatCommand{Kind: ChatCommandPrompt, Brief: "shop", Text: "dark"},
			c:      ChatContext{Brief: ChatBriefView{ResourceVersion: "7", Prompts: []string{"one"}}},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "add a prompt to a Brief",
			body: `[{"op":"add","path":"/spec/prompts/-","value":"dark"}]`},
		{name: "prompt to a Brief without", cmd: ChatCommand{Kind: ChatCommandPrompt, Brief: "shop", Text: "dark"},
			c:      ChatContext{Brief: ChatBriefView{ResourceVersion: "7"}},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "add a prompt to a Brief",
			body: `[{"op":"test","path":"/metadata/resourceVersion","value":"7"},` +
				`{"op":"add","path":"/spec/prompts","value":["dark"]}]`},
		{name: "prompt to a full Brief", cmd: ChatCommand{Kind: ChatCommandPrompt, Brief: "shop", Text: "dark"},
			c:      ChatContext{Brief: ChatBriefView{ResourceVersion: "7", Prompts: full, PromptsRetired: 4, PromptsPlanned: 16}},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "add a prompt to a Brief",
			body: `[{"op":"test","path":"/spec/prompts","value":` + string(mustJSON(full)) + `},` +
				`{"op":"replace","path":"/spec/prompts","value":` + string(mustJSON(append(slices.Clone(full[2:]), "dark"))) + `},` +
				`{"op":"add","path":"/spec/promptsRetired","value":6}]`},
		{name: "pause", cmd: ChatCommand{Kind: ChatCommandPause, Brief: "shop"},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "pause or resume a Brief",
			body: `[{"op":"add","path":"/spec/paused","value":true}]`},
		{name: "resume", cmd: ChatCommand{Kind: ChatCommandResume, Brief: "shop"},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "pause or resume a Brief",
			body: `[{"op":"add","path":"/spec/paused","value":false}]`},
		{name: "resume with its runs paused", cmd: ChatCommand{Kind: ChatCommandResume, Brief: "shop"},
			c:      ChatContext{Brief: ChatBriefView{PauseRuns: true}},
			method: "PATCH", res: "briefs", obj: "shop", ctype: ChatJSONPatch, what: "pause or resume a Brief",
			body: `[{"op":"add","path":"/spec/paused","value":false},{"op":"add","path":"/spec/pauseRuns","value":false}]`},
		{name: "answer", cmd: ChatCommand{Kind: ChatCommandAnswer, Text: "Only logged-in users."},
			c: ChatContext{Item: ChatItem{Event: ChatEventTriageQuestion, Name: "shop-12-triage"},
				By: "ada@example.com"},
			method: "PATCH", res: "tasks", obj: "shop-12-triage", ctype: ChatMergePatch, what: "answer a parked issue",
			body: `{"metadata":{"annotations":{"theos.factory.io/answer":"Only logged-in users.",` +
				`"theos.factory.io/answer-by":"ada@example.com"}}}`},
		{name: "approve a spec", cmd: ChatCommand{Kind: ChatCommandApproveSpec},
			c: ChatContext{Item: ChatItem{Event: ChatEventSpecWaiting, Name: "api-12-spec", Commit: "abc123"},
				By: "ada@example.com"},
			method: "PATCH", res: "tasks", obj: "api-12-spec", ctype: ChatMergePatch, what: "approve a spec",
			body: `{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc123",` +
				`"theos.factory.io/approve-spec-by":"ada@example.com"}}}`},
		{name: "changes", cmd: ChatCommand{Kind: ChatCommandChanges, Text: "name the tokens"},
			c: ChatContext{Item: ChatItem{Event: ChatEventSpecWaiting, Name: "api-12-spec", Commit: "abc123"},
				By: "ada@example.com"},
			method: "PATCH", res: "tasks", obj: "api-12-spec", ctype: ChatMergePatch, what: "ask for changes to a spec",
			body: `{"metadata":{"annotations":{"theos.factory.io/spec-changes":"abc123 name the tokens",` +
				`"theos.factory.io/spec-changes-by":"ada@example.com"}}}`},
		// #92: a change its Crew's approval rules hold takes the same words as a spec.
		{name: "approve a waiting change", cmd: ChatCommand{Kind: ChatCommandApproveSpec},
			c: ChatContext{Item: ChatItem{Event: ChatEventChangeWaiting, Name: "api-12", Commit: "abc123"},
				By: "ada@example.com"},
			method: "PATCH", res: "tasks", obj: "api-12", ctype: ChatMergePatch, what: "approve a spec",
			body: `{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc123",` +
				`"theos.factory.io/approve-spec-by":"ada@example.com"}}}`},
		{name: "changes to a waiting change", cmd: ChatCommand{Kind: ChatCommandChanges, Text: "keep the old column"},
			c: ChatContext{Item: ChatItem{Event: ChatEventChangeWaiting, Name: "api-12", Commit: "abc123"},
				By: "ada@example.com"},
			method: "PATCH", res: "tasks", obj: "api-12", ctype: ChatMergePatch, what: "ask for changes to a spec",
			body: `{"metadata":{"annotations":{"theos.factory.io/spec-changes":"abc123 keep the old column",` +
				`"theos.factory.io/spec-changes-by":"ada@example.com"}}}`},
		{name: "approve a deploy with no approval yet", cmd: ChatCommand{Kind: ChatCommandApproveDeploy, Version: "v1.2.0"},
			c: ChatContext{Item: ChatItem{Event: ChatEventDeployWaiting, Name: "api", Target: "prod"},
				Deploy: []ChatDeployView{{Target: "staging"}, {Target: "prod", After: "staging"}}},
			method: "PATCH", res: "containers", obj: "api", ctype: ChatJSONPatch, what: "approve a deploy",
			body: `[{"op":"test","path":"/spec/deploy/1/target","value":"prod"},` +
				`{"op":"test","path":"/spec/deploy/1","value":{"target":"prod","after":"staging"}},` +
				`{"op":"add","path":"/spec/deploy/1/approved","value":"v1.2.0"}]`},
		{name: "approve a deploy over an older approval", cmd: ChatCommand{Kind: ChatCommandApproveDeploy, Version: "v1.3.0"},
			c: ChatContext{Item: ChatItem{Event: ChatEventDeployWaiting, Name: "api", Target: "prod"},
				Deploy: []ChatDeployView{{Target: "prod", Approved: "v1.2.0"}}},
			method: "PATCH", res: "containers", obj: "api", ctype: ChatJSONPatch, what: "approve a deploy",
			body: `[{"op":"test","path":"/spec/deploy/0/target","value":"prod"},` +
				`{"op":"test","path":"/spec/deploy/0/approved","value":"v1.2.0"},` +
				`{"op":"add","path":"/spec/deploy/0/approved","value":"v1.3.0"}]`},
		{name: "retry", cmd: ChatCommand{Kind: ChatCommandRetry},
			c:      ChatContext{Item: ChatItem{Event: ChatEventContainerFailed, Name: "api"}, Now: now},
			method: "PATCH", res: "containers", obj: "api", ctype: ChatMergePatch, what: "retry a Container",
			body: `{"metadata":{"annotations":{"theos.factory.io/retry":"1762300000"}}}`},
	}
	reached := map[string]bool{}
	for _, tt := range tests {
		req, writes, err := ChatCommandRequest(tt.cmd, tt.c)
		if err != nil || !writes {
			t.Errorf("%s: ChatCommandRequest = %t, %v", tt.name, writes, err)
			continue
		}
		if req.Method != tt.method || req.Resource != tt.res || req.Name != tt.obj || req.ContentType != tt.ctype {
			t.Errorf("%s: request %s %s %q %s, want %s %s %q %s", tt.name, req.Method, req.Resource, req.Name,
				req.ContentType, tt.method, tt.res, tt.obj, tt.ctype)
		}
		if !jsonEqual(t, req.Body, tt.body) {
			t.Errorf("%s: body\n%s\nwant\n%s", tt.name, req.Body, tt.body)
		}
		var matched []string
		for _, a := range chatAllowlist {
			body, _ := decodeStrict(req.Body)
			if a.method == req.Method && a.resource == req.Resource && a.contentType == req.ContentType && a.check(body) {
				matched = append(matched, a.what)
			}
		}
		if len(matched) != 1 || matched[0] != tt.what {
			t.Errorf("%s: the allowlist entries %q take it, want exactly %q", tt.name, matched, tt.what)
		}
		reached[tt.what] = true
	}
	for _, a := range chatAllowlist {
		if !reached[a.what] {
			t.Errorf("no command reaches allowlist entry %q", a.what)
		}
	}
	for _, kind := range []ChatCommandKind{ChatCommandStatus, ChatCommandHelp} {
		if _, writes, err := ChatCommandRequest(ChatCommand{Kind: kind}, ChatContext{}); writes || err != nil {
			t.Errorf("%s writes %t, %v; want nothing", kind, writes, err)
		}
	}
	if req := (ChatRequest{Resource: "briefs", Name: "shop"}); req.Path("webshop") !=
		"/apis/theos.factory.io/v1beta1/namespaces/webshop/briefs/shop" {
		t.Errorf("Path = %s", req.Path("webshop"))
	}
}

// A thread command on a thread about something else, a deploy the
// Container no longer has, or a full Brief that can retire nothing writes
// nothing.
func TestChatCommandRequestRefuses(t *testing.T) {
	full := make([]string, 50)
	for i := range full {
		full[i] = "p"
	}
	tests := []struct {
		name string
		cmd  ChatCommand
		c    ChatContext
	}{
		{"approve on a deploy thread", ChatCommand{Kind: ChatCommandApproveSpec},
			ChatContext{Item: ChatItem{Event: ChatEventDeployWaiting, Name: "api", Target: "prod"}}},
		{"approve a spec with no commit", ChatCommand{Kind: ChatCommandApproveSpec},
			ChatContext{Item: ChatItem{Event: ChatEventSpecWaiting, Name: "api-1-spec"}, By: "ada"}},
		{"approve a spec as nobody", ChatCommand{Kind: ChatCommandApproveSpec},
			ChatContext{Item: ChatItem{Event: ChatEventSpecWaiting, Name: "api-1-spec", Commit: "abc"}}},
		{"approve a change that no longer waits", ChatCommand{Kind: ChatCommandApproveSpec},
			ChatContext{Item: ChatItem{Event: ChatEventChangeWaiting, Name: "api-1"}, By: "ada"}},
		{"approve a change as nobody", ChatCommand{Kind: ChatCommandApproveSpec},
			ChatContext{Item: ChatItem{Event: ChatEventChangeWaiting, Name: "api-1", Commit: "abc"}}},
		{"changes on a deploy thread", ChatCommand{Kind: ChatCommandChanges, Text: "x"},
			ChatContext{Item: ChatItem{Event: ChatEventDeployWaiting, Name: "api", Target: "prod"}, By: "ada"}},
		{"answer as nobody", ChatCommand{Kind: ChatCommandAnswer, Text: "x"},
			ChatContext{Item: ChatItem{Event: ChatEventTriageQuestion, Name: "api-1-triage"}}},
		{"retry outside a thread", ChatCommand{Kind: ChatCommandRetry}, ChatContext{}},
		{"answer on a spec thread", ChatCommand{Kind: ChatCommandAnswer, Text: "x"},
			ChatContext{Item: ChatItem{Event: ChatEventSpecWaiting, Name: "api-1-spec", Commit: "abc"}}},
		{"approve a target no longer there", ChatCommand{Kind: ChatCommandApproveDeploy, Version: "v1"},
			ChatContext{Item: ChatItem{Event: ChatEventDeployWaiting, Name: "api", Target: "prod"},
				Deploy: []ChatDeployView{{Target: "staging"}}}},
		{"a thread item with a name that is no object's", ChatCommand{Kind: ChatCommandRetry},
			ChatContext{Item: ChatItem{Event: ChatEventContainerFailed, Name: "../../secrets/x"}}},
		{"prompt a full Brief", ChatCommand{Kind: ChatCommandPrompt, Brief: "shop", Text: "x"},
			ChatContext{Brief: ChatBriefView{Prompts: full, PromptsPlanned: 5}}},
		{"an unknown command", ChatCommand{Kind: "delete"}, ChatContext{}},
	}
	for _, tt := range tests {
		if req, writes, err := ChatCommandRequest(tt.cmd, tt.c); err == nil || writes {
			t.Errorf("%s: ChatCommandRequest = %+v, %t, nil; want an error", tt.name, req, writes)
		}
	}
}

// Anything but the allowlist's requests is refused: other kinds, methods,
// fields, annotations and shapes, and bodies a server could read otherwise.
func TestCheckChatRequestRefuses(t *testing.T) {
	merge := func(res, name, body string) ChatRequest {
		return ChatRequest{Method: "PATCH", Resource: res, Name: name, ContentType: ChatMergePatch, Body: []byte(body)}
	}
	patch := func(res, name, body string) ChatRequest {
		return ChatRequest{Method: "PATCH", Resource: res, Name: name, ContentType: ChatJSONPatch, Body: []byte(body)}
	}
	post := func(res, body string) ChatRequest {
		return ChatRequest{Method: "POST", Resource: res, ContentType: ChatJSON, Body: []byte(body)}
	}
	retry := `{"metadata":{"annotations":{"theos.factory.io/retry":"1"}}}`
	brief := `{"apiVersion":"theos.factory.io/v1beta1","kind":"Brief","metadata":{"generateName":"brief-"},` +
		`"spec":{"idea":"x"}}`
	tests := []struct {
		name string
		req  ChatRequest
	}{
		{"a delete", ChatRequest{Method: "DELETE", Resource: "briefs", Name: "shop", ContentType: ChatJSON, Body: []byte(`{}`)}},
		{"a put", ChatRequest{Method: "PUT", Resource: "containers", Name: "api", ContentType: ChatMergePatch, Body: []byte(retry)}},
		{"a Secret", merge("secrets", "api", retry)},
		{"a Crew's annotation", merge("crews", "api", retry)},
		{"another annotation", merge("containers", "api", `{"metadata":{"annotations":{"theos.factory.io/standard-take":"1"}}}`)},
		{"two annotations", merge("containers", "api",
			`{"metadata":{"annotations":{"theos.factory.io/retry":"1","theos.factory.io/pause":"1"}}}`)},
		{"an annotation and a label", merge("containers", "api",
			`{"metadata":{"annotations":{"theos.factory.io/retry":"1"},"labels":{"a":"b"}}}`)},
		{"an annotation and the spec", merge("containers", "api",
			`{"metadata":{"annotations":{"theos.factory.io/retry":"1"}},"spec":{"image":"evil"}}`)},
		{"an annotation that isn't a string", merge("containers", "api", `{"metadata":{"annotations":{"theos.factory.io/retry":1}}}`)},
		{"a merge patch as JSON", ChatRequest{Method: "PATCH", Resource: "containers", Name: "api", ContentType: ChatJSON, Body: []byte(retry)}},
		{"a name that isn't one", merge("containers", "../secrets/x", retry)},
		{"no name", merge("containers", "", retry)},
		{"a duplicate key", merge("containers", "api",
			`{"metadata":{"annotations":{"theos.factory.io/retry":"1"}},"metadata":{"labels":{"a":"b"}}}`)},
		{"data after the body", merge("containers", "api", retry+`{}`)},
		{"no JSON", merge("containers", "api", `retry`)},
		{"a Brief with a name too", post("briefs", `{"apiVersion":"theos.factory.io/v1beta1","kind":"Brief",`+
			`"metadata":{"name":"a","generateName":"b"},"spec":{"idea":"x"}}`)},
		{"a Brief with labels", post("briefs", `{"apiVersion":"theos.factory.io/v1beta1","kind":"Brief",`+
			`"metadata":{"generateName":"brief-","labels":{"a":"b"}},"spec":{"idea":"x"}}`)},
		{"a Brief with more than an idea", post("briefs", `{"apiVersion":"theos.factory.io/v1beta1","kind":"Brief",`+
			`"metadata":{"generateName":"brief-"},"spec":{"idea":"x","autopilot":"Full"}}`)},
		{"a Brief at v1alpha1", post("briefs", strings.Replace(brief, "v1beta1", "v1alpha1", 1))},
		{"a Task", post("tasks", strings.Replace(brief, `"Brief"`, `"Task"`, 1))},
		{"a POST with a name", ChatRequest{Method: "POST", Resource: "briefs", Name: "shop", ContentType: ChatJSON, Body: []byte(brief)}},
		{"a Brief's autopilot", patch("briefs", "shop", `[{"op":"add","path":"/spec/autopilot","value":"Full"}]`)},
		{"a Brief's idea", patch("briefs", "shop", `[{"op":"replace","path":"/spec/idea","value":"x"}]`)},
		{"a prompt removed", patch("briefs", "shop", `[{"op":"remove","path":"/spec/prompts/0"}]`)},
		{"a prompt that isn't a string", patch("briefs", "shop", `[{"op":"add","path":"/spec/prompts/-","value":1}]`)},
		{"a prompt list unguarded", patch("briefs", "shop", `[{"op":"add","path":"/spec/prompts","value":["x"]}]`)},
		{"a prompt list after an empty test", patch("briefs", "shop",
			`[{"op":"test","path":"/metadata/resourceVersion","value":""},{"op":"add","path":"/spec/prompts","value":["x"]}]`)},
		{"paused twice", patch("briefs", "shop",
			`[{"op":"add","path":"/spec/paused","value":true},{"op":"add","path":"/spec/paused","value":false}]`)},
		{"paused as a string", patch("briefs", "shop", `[{"op":"add","path":"/spec/paused","value":"true"}]`)},
		{"an op with a from", patch("briefs", "shop", `[{"op":"add","path":"/spec/paused","value":true,"from":"/x"}]`)},
		{"an empty patch", patch("briefs", "shop", `[]`)},
		{"a Container's checks", patch("containers", "api", `[{"op":"add","path":"/spec/checks","value":"none"}]`)},
		{"an approval with no test", patch("containers", "api", `[{"op":"add","path":"/spec/deploy/0/approved","value":"v1"}]`)},
		{"an approval at another entry", patch("containers", "api",
			`[{"op":"test","path":"/spec/deploy/0/target","value":"prod"},`+
				`{"op":"test","path":"/spec/deploy/0/approved","value":"v0"},`+
				`{"op":"add","path":"/spec/deploy/1/approved","value":"v1"}]`)},
		{"an approval of the version field", patch("containers", "api",
			`[{"op":"test","path":"/spec/deploy/0/target","value":"prod"},`+
				`{"op":"test","path":"/spec/deploy/0/approved","value":"v0"},`+
				`{"op":"add","path":"/spec/deploy/0/version","value":"v1"}]`)},
		{"an approval testing an entry with more fields", patch("containers", "api",
			`[{"op":"test","path":"/spec/deploy/0/target","value":"prod"},`+
				`{"op":"test","path":"/spec/deploy/0","value":{"target":"prod","approved":"v0"}},`+
				`{"op":"add","path":"/spec/deploy/0/approved","value":"v1"}]`)},
		{"a spec approval on a Container", merge("containers", "api",
			`{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc","theos.factory.io/approve-spec-by":"ada"}}}`)},
		{"a spec approval naming nobody", merge("tasks", "api-1-spec",
			`{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc"}}}`)},
		{"a spec approval naming an empty user", merge("tasks", "api-1-spec",
			`{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc","theos.factory.io/approve-spec-by":""}}}`)},
		{"an answer's -by alone", merge("tasks", "api-1-triage",
			`{"metadata":{"annotations":{"theos.factory.io/answer-by":"ada"}}}`)},
		{"an approval with another's -by", merge("tasks", "api-1-spec",
			`{"metadata":{"annotations":{"theos.factory.io/approve-spec":"abc","theos.factory.io/answer-by":"ada"}}}`)},
		{"a retry on a Task", merge("tasks", "api-1", retry)},
	}
	for _, tt := range tests {
		if err := CheckChatRequest(tt.req); !errors.Is(err, ErrChatRefused) {
			t.Errorf("%s: CheckChatRequest = %v, want it refused", tt.name, err)
		}
	}
}

func TestParseChatSpecChanges(t *testing.T) {
	commit, text, ok := ParseChatSpecChanges("abc123 name the tokens: all of them")
	if !ok || commit != "abc123" || text != "name the tokens: all of them" {
		t.Errorf("ParseChatSpecChanges = %q, %q, %t", commit, text, ok)
	}
	for _, v := range []string{"", "abc123", " text", "abc123 "} {
		if _, _, ok := ParseChatSpecChanges(v); ok {
			t.Errorf("ParseChatSpecChanges(%q) read it", v)
		}
	}
}
