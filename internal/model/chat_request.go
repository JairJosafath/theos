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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The methods and JSON patch operations of the requests, and the JSON types
// their checks name.
const (
	methodPOST       = "POST"
	methodPATCH      = "PATCH"
	opTest           = "test"
	opAdd            = "add"
	opReplace        = "replace"
	pathPrompts      = "/spec/prompts"
	fieldTarget      = "target"
	fieldMetadata    = "metadata"
	fieldAnnotations = "annotations"
	jsonBoolean      = "boolean"
	jsonString       = "string"
)

// ChatRequest is the one write a chat command becomes, made by theos-chat
// as the person who gave it, in the Chat's namespace (docs/plans/done/chat-matrix.md,
// "The CRDs stay the truth"). CheckChatRequest holds it to the allowlist.
type ChatRequest struct {
	// Method is POST or PATCH.
	Method string
	// Resource is the theos.factory.io plural: briefs, containers or tasks.
	Resource string
	// Name is the object's name; "" for a POST.
	Name string
	// ContentType is the body's media type.
	ContentType string
	// Body is the JSON sent.
	Body []byte
}

// The media types of the requests.
const (
	ChatJSON       = "application/json"
	ChatJSONPatch  = "application/json-patch+json"
	ChatMergePatch = "application/merge-patch+json"
)

// The annotations a chat command writes. Retry is the UI's; the other three
// a person writes from chat alone, and the operator reads them as the
// person's word on the item (docs/plans/done/chat-matrix.md, "Specs approved
// from chat").
const (
	// ChatAnnotationRetry on a Container scaffolds it again.
	ChatAnnotationRetry = "theos.factory.io/retry"
	// ChatAnnotationApproveSpec on a Spec Task names the head commit a person
	// approves.
	ChatAnnotationApproveSpec = "theos.factory.io/approve-spec"
	// ChatAnnotationSpecChanges on a Spec Task is the head commit, a space
	// and the changes a person asks of it (ChatSpecChanges).
	ChatAnnotationSpecChanges = "theos.factory.io/spec-changes"
	// ChatAnnotationAnswer on a Triage Task is a person's answer to its
	// parked issue's question, for the issue.
	ChatAnnotationAnswer = "theos.factory.io/answer"
)

// ChatAnnotationBySuffix makes each of the three chat annotations' companion,
// <annotation>-by: who wrote it, as their Kubernetes user name. The
// admission policy (config/policy/task_chat_annotations_policy.yaml) refuses
// a write that sets or changes an annotation unless its companion names the
// writer, request.userInfo.username, and any write of them by a service
// account, so the companion can't be forged; the operator counts no
// annotation without one.
const ChatAnnotationBySuffix = "-by"

// ChatAnnotationBy is annotation's companion naming who wrote it.
func ChatAnnotationBy(annotation string) string { return annotation + ChatAnnotationBySuffix }

// ChatAnnotations are the three annotations a person writes from chat
// alone, which the admission policy guards.
var ChatAnnotations = []string{ChatAnnotationApproveSpec, ChatAnnotationSpecChanges, ChatAnnotationAnswer}

// The resources a command writes.
const (
	chatBriefs     = "briefs"
	chatContainers = "containers"
	chatTasks      = "tasks"
)

// ChatAPIVersion is the version every request's object is at.
const ChatAPIVersion = "theos.factory.io/v1beta1"

// Path is the request's API path in namespace.
func (r ChatRequest) Path(namespace string) string {
	p := "/apis/" + ChatAPIVersion + "/namespaces/" + namespace + "/" + r.Resource
	if r.Name != "" {
		p += "/" + r.Name
	}
	return p
}

// ChatBriefView is what a prompt's write needs of the Brief as just read.
type ChatBriefView struct {
	ResourceVersion string
	Prompts         []string
	// PromptsRetired is spec.promptsRetired; PromptsPlanned is
	// status.promptsPlanned.
	PromptsRetired, PromptsPlanned int
	// PauseRuns is spec.pauseRuns: a resume unsets it too.
	PauseRuns bool
}

// ChatDeployView is one of a Container's spec.deploy entries as just read.
type ChatDeployView struct {
	Target, After, Version, Approved string
}

// ChatItem is what a thread is about, as theos-chat recorded it when it
// posted the root: its event, the object's name, and for a spec the head
// commit shown, for a deploy the DeployTarget.
type ChatItem struct {
	Event  ChatEventKind `json:"event,omitempty"`
	Name   string        `json:"name,omitempty"`
	Commit string        `json:"commit,omitempty"`
	Target string        `json:"target,omitempty"`
}

// ChatContext is what a command's write is made from: the thread's item,
// the object as just read, and who gives it.
type ChatContext struct {
	Item ChatItem
	// By is the Kubernetes user the person acts as: the chat annotations name
	// them in their companion (ChatAnnotationBy).
	By     string
	Brief  ChatBriefView
	Deploy []ChatDeployView
	// Now stamps a retry.
	Now time.Time
}

// ChatCommandRequest is the write cmd becomes, or false for a command that
// writes nothing (status, help). Every request it returns passes
// CheckChatRequest.
func ChatCommandRequest(cmd ChatCommand, c ChatContext) (ChatRequest, bool, error) {
	var (
		req ChatRequest
		err error
	)
	switch cmd.Kind {
	case ChatCommandStatus, ChatCommandHelp:
		return ChatRequest{}, false, nil
	case ChatCommandBrief:
		req = ChatCreateBrief(cmd.Text)
	case ChatCommandPrompt:
		req, err = ChatAppendPrompt(cmd.Brief, c.Brief, cmd.Text)
	case ChatCommandPause:
		req = ChatPauseBrief(cmd.Brief, true, false)
	case ChatCommandResume:
		req = ChatPauseBrief(cmd.Brief, false, c.Brief.PauseRuns)
	case ChatCommandAnswer:
		err = chatItemIs(c, ChatEventTriageQuestion, cmd.Kind)
		req = ChatAnswer(c.Item.Name, cmd.Text, c.By)
	case ChatCommandApproveSpec:
		err = chatItemWaits(c, cmd.Kind)
		req = ChatApproveSpec(c.Item.Name, c.Item.Commit, c.By)
	case ChatCommandChanges:
		err = chatItemWaits(c, cmd.Kind)
		req = ChatSpecChanges(c.Item.Name, c.Item.Commit, cmd.Text, c.By)
	case ChatCommandApproveDeploy:
		if err = chatItemIs(c, ChatEventDeployWaiting, cmd.Kind); err == nil {
			req, err = ChatApproveDeploy(c.Item.Name, c.Deploy, c.Item.Target, cmd.Version)
		}
	case ChatCommandRetry:
		err = chatItemIs(c, ChatEventContainerFailed, cmd.Kind)
		req = ChatRetryContainer(c.Item.Name, c.Now)
	default:
		return ChatRequest{}, false, fmt.Errorf("%w: no write for command %q", ErrChatInvalidCommand, cmd.Kind)
	}
	if err != nil {
		return ChatRequest{}, false, err
	}
	if err := CheckChatRequest(req); err != nil {
		return ChatRequest{}, false, err
	}
	return req, true, nil
}

// chatItemWaits refuses an approval or a request for changes outside the
// thread of a pull request that waits for a person: a spec's, or a change's
// its Crew's approval rules hold (#92). In a deploy's thread, which shares
// the reviews room with a change's, it says what a deploy's approval takes.
func chatItemWaits(c ChatContext, cmd ChatCommandKind) error {
	switch c.Item.Event {
	case ChatEventChangeWaiting:
		return chatItemIs(c, ChatEventChangeWaiting, cmd)
	case ChatEventDeployWaiting:
		return fmt.Errorf("%w: approve <version>, the release the thread names, such as v1.2.0",
			ErrChatInvalidCommand)
	}
	return chatItemIs(c, ChatEventSpecWaiting, cmd)
}

// chatWaits reports whether event is a pull request waiting for a person.
func chatWaits(event ChatEventKind) bool {
	return event == ChatEventSpecWaiting || event == ChatEventChangeWaiting
}

// chatItemIs refuses a thread command on a thread about something else, and
// one of the chat annotations from nobody.
func chatItemIs(c ChatContext, want ChatEventKind, cmd ChatCommandKind) error {
	item := c.Item
	if item.Event != want || item.Name == "" {
		return fmt.Errorf("%w: %s answers a thread the factory started about it", ErrChatInvalidCommand, cmd)
	}
	if chatWaits(want) && item.Commit == "" {
		return fmt.Errorf("%w: nothing in this thread waits for approval now", ErrChatInvalidCommand)
	}
	if (chatWaits(want) || want == ChatEventTriageQuestion) && c.By == "" {
		return fmt.Errorf("%w: %s needs the person's Kubernetes user", ErrChatInvalidCommand, cmd)
	}
	return nil
}

// jsonOp is one JSON patch operation.
type jsonOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // only maps, slices, strings, numbers and booleans
	}
	return data
}

// ChatCreateBrief makes a Brief of idea, named by the API server.
func ChatCreateBrief(idea string) ChatRequest {
	return ChatRequest{Method: methodPOST, Resource: chatBriefs, ContentType: ChatJSON, Body: mustJSON(map[string]any{
		"apiVersion": ChatAPIVersion, "kind": "Brief",
		fieldMetadata: map[string]any{"generateName": "brief-"},
		"spec":        map[string]any{"idea": idea},
	})}
}

// ChatAppendPrompt appends prompt to Brief name as the UI does
// (ui/server.mjs, addPrompt): a Brief without prompts gets the list, after a
// test of the resourceVersion just read; a full one retires its oldest
// planned prompts in the same write, after a test of the list just read
// (PromptsToRetire); one that can retire none is refused.
func ChatAppendPrompt(name string, b ChatBriefView, prompt string) (ChatRequest, error) {
	req := ChatRequest{Method: methodPATCH, Resource: chatBriefs, Name: name, ContentType: ChatJSONPatch}
	switch n := len(b.Prompts); {
	case n == 0:
		req.Body = mustJSON([]jsonOp{{opTest, "/metadata/resourceVersion", b.ResourceVersion},
			{opAdd, pathPrompts, []string{prompt}}})
	case n < chatMaxPrompts:
		req.Body = mustJSON([]jsonOp{{opAdd, "/spec/prompts/-", prompt}})
	default:
		retire := PromptsToRetire(b.PromptsRetired, n, b.PromptsPlanned)
		if retire == 0 {
			return ChatRequest{}, fmt.Errorf("%w: Brief %s lists %d prompts, the most it takes, and no round has "+
				"planned enough of them to retire one; wait for its next round", ErrChatInvalidCommand, name, n)
		}
		kept := append(slices.Clone(b.Prompts[retire:]), prompt)
		req.Body = mustJSON([]jsonOp{{opTest, pathPrompts, b.Prompts}, {opReplace, pathPrompts, kept},
			{opAdd, "/spec/promptsRetired", b.PromptsRetired + retire}})
	}
	return req, nil
}

// chatMaxPrompts is how many prompts a Brief lists at most (spec.prompts'
// CEL rule).
const chatMaxPrompts = 50

// ChatPauseBrief pauses (true) or resumes (false) Brief name as the UI does
// (ui/src/k8s/briefs.ts, pausedPatch): a resume while its runs are paused
// unsets pauseRuns too, as the CRD takes pauseRuns only with paused.
func ChatPauseBrief(name string, paused, runsPaused bool) ChatRequest {
	ops := []jsonOp{{opAdd, "/spec/paused", paused}}
	if !paused && runsPaused {
		ops = append(ops, jsonOp{opAdd, "/spec/pauseRuns", false})
	}
	return ChatRequest{Method: methodPATCH, Resource: chatBriefs, Name: name, ContentType: ChatJSONPatch, Body: mustJSON(ops)}
}

// chatAnnotate is a merge patch setting one annotation.
func chatAnnotate(resource, name, key, value string) ChatRequest {
	return ChatRequest{Method: methodPATCH, Resource: resource, Name: name, ContentType: ChatMergePatch,
		Body: mustJSON(map[string]any{fieldMetadata: map[string]any{fieldAnnotations: map[string]string{key: value}}})}
}

// ChatRetryContainer scaffolds Container name again, as the UI's retry
// does: the retry annotation set to the time, in Unix seconds.
func ChatRetryContainer(name string, now time.Time) ChatRequest {
	return chatAnnotate(chatContainers, name, ChatAnnotationRetry, strconv.FormatInt(now.Unix(), 10))
}

// chatAnnotateBy is a merge patch on Task name setting one of the chat
// annotations and its companion naming by, the Kubernetes user who writes
// it.
func chatAnnotateBy(name, key, value, by string) ChatRequest {
	return ChatRequest{Method: methodPATCH, Resource: chatTasks, Name: name, ContentType: ChatMergePatch,
		Body: mustJSON(map[string]any{fieldMetadata: map[string]any{fieldAnnotations: map[string]string{
			key: value, ChatAnnotationBy(key): by}}})}
}

// ChatApproveSpec approves the spec of Spec Task name at commit, as by.
func ChatApproveSpec(name, commit, by string) ChatRequest {
	return chatAnnotateBy(name, ChatAnnotationApproveSpec, commit, by)
}

// ChatSpecChanges asks for changes to the spec of Spec Task name at commit,
// as by.
func ChatSpecChanges(name, commit, text, by string) ChatRequest {
	return chatAnnotateBy(name, ChatAnnotationSpecChanges, commit+" "+text, by)
}

// ParseChatSpecChanges reads the spec-changes annotation's value: the
// commit asked of and the changes.
func ParseChatSpecChanges(value string) (commit, text string, ok bool) {
	commit, text, ok = strings.Cut(value, " ")
	return commit, text, ok && commit != "" && text != ""
}

// ChatAnswer answers the question of Triage Task name's parked issue, as by.
func ChatAnswer(name, text, by string) ChatRequest {
	return chatAnnotateBy(name, ChatAnnotationAnswer, text, by)
}

// ChatApproveDeploy lets version into Container name's deploy entry for
// target, as the UI does (ui/server.mjs, approvesDeploy): a test that the
// entry still names target, a test of the approval as read (or, without
// one, of the whole entry, since JSON Patch can't test a missing member),
// then the approval.
func ChatApproveDeploy(name string, entries []ChatDeployView, target, version string) (ChatRequest, error) {
	i := slices.IndexFunc(entries, func(e ChatDeployView) bool { return e.Target == target })
	if i < 0 || i > 99 {
		return ChatRequest{}, fmt.Errorf("%w: Container %s no longer deploys to %s", ErrChatInvalidCommand, name, target)
	}
	e := entries[i]
	at := fmt.Sprintf("/spec/deploy/%d", i)
	was := jsonOp{opTest, at + "/approved", e.Approved}
	if e.Approved == "" {
		entry := map[string]string{fieldTarget: e.Target}
		if e.After != "" {
			entry["after"] = e.After
		}
		if e.Version != "" {
			entry["version"] = e.Version
		}
		was = jsonOp{opTest, at, entry}
	}
	return ChatRequest{Method: methodPATCH, Resource: chatContainers, Name: name, ContentType: ChatJSONPatch,
		Body: mustJSON([]jsonOp{{opTest, at + "/target", target}, was, {opAdd, at + "/approved", version}})}, nil
}

// ErrChatRefused is a request outside the allowlist.
var ErrChatRefused = errors.New("not a write chat makes")

// chatAllowed is one write the allowlist takes: a method, a resource, a
// media type and a check of the body's shape. Like the UI's table
// (ui/server.mjs, ACTIONS), each is one request and the check is of shape,
// not of values: limits and enums are the CRD's.
type chatAllowed struct {
	what                          string
	method, resource, contentType string
	check                         func(body any) bool
}

// chatAllowlist is every write a command may become: each is the UI's
// entry it names, or one of the plan's chat-only annotations, and no wider.
var chatAllowlist = []chatAllowed{
	// The UI's "create a Brief", with only a name and an idea.
	{"create a Brief", methodPOST, chatBriefs, ChatJSON, createsBrief},
	// The UI's "add a prompt", in the three shapes its addPrompt sends.
	{"add a prompt to a Brief", methodPATCH, chatBriefs, ChatJSONPatch, appendsPrompt},
	// The UI's "pause or resume a Brief".
	{"pause or resume a Brief", methodPATCH, chatBriefs, ChatJSONPatch,
		addsFields(map[string]string{"/spec/paused": jsonBoolean, "/spec/pauseRuns": jsonBoolean})},
	// The UI's "set annotation theos.factory.io/retry on a Container".
	{"retry a Container", methodPATCH, chatContainers, ChatMergePatch, setsAnnotation(ChatAnnotationRetry)},
	// The UI's "approve a version at one of a Container's spec.deploy entries".
	{"approve a deploy", methodPATCH, chatContainers, ChatJSONPatch, approvesDeploy},
	// The plan's spec approval, and its changes and triage answer.
	// Each with its companion naming the writer, which the admission policy
	// holds to the request's user.
	{"approve a spec", methodPATCH, chatTasks, ChatMergePatch, setsAnnotationBy(ChatAnnotationApproveSpec)},
	{"ask for changes to a spec", methodPATCH, chatTasks, ChatMergePatch, setsAnnotationBy(ChatAnnotationSpecChanges)},
	{"answer a parked issue", methodPATCH, chatTasks, ChatMergePatch, setsAnnotationBy(ChatAnnotationAnswer)},
}

// CheckChatRequest refuses, wrapping ErrChatRefused, any request but the
// allowlist's: another method, resource, media type or body shape, a POST
// with a name or a PATCH without a valid one.
func CheckChatRequest(r ChatRequest) error {
	if (r.Method == methodPOST && r.Name != "") || (r.Method != methodPOST && !chatValidName(r.Name)) {
		return fmt.Errorf("%w: %s %s %q", ErrChatRefused, r.Method, r.Resource, r.Name)
	}
	body, err := decodeStrict(r.Body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChatRefused, err)
	}
	for _, a := range chatAllowlist {
		if a.method == r.Method && a.resource == r.Resource && a.contentType == r.ContentType && a.check(body) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %s with %s", ErrChatRefused, r.Method, r.Resource, r.ContentType)
}

// decodeStrict decodes one JSON value, numbers as json.Number, refusing a
// duplicate key anywhere and anything after the value: a body the API
// server could read otherwise than the check did.
func decodeStrict(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		obj := map[string]any{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, _ := k.(string)
			if _, dup := obj[key]; dup {
				return nil, fmt.Errorf("key %q twice", key)
			}
			if obj[key], err = decodeValue(dec); err != nil {
				return nil, err
			}
		}
		_, err = dec.Token()
		return obj, err
	case json.Delim('['):
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err = dec.Token()
		return arr, err
	}
	return tok, nil
}

// hasType reports whether v has the JSON type typ.
func hasType(v any, typ string) bool {
	switch typ {
	case jsonBoolean:
		_, ok := v.(bool)
		return ok
	case jsonString:
		_, ok := v.(string)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(n.String(), 10, 64)
		return err == nil
	case "strings":
		arr, ok := v.([]any)
		return ok && !slices.ContainsFunc(arr, func(x any) bool { return !hasType(x, jsonString) })
	}
	return false
}

// keysAre reports whether obj is an object with exactly keys.
func keysAre(v any, keys ...string) (map[string]any, bool) {
	obj, ok := v.(map[string]any)
	if !ok || len(obj) != len(keys) {
		return nil, false
	}
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			return nil, false
		}
	}
	return obj, true
}

// patchOp reads one JSON patch operation: op, path and value, all three.
func patchOp(v any) (op, path string, value any, ok bool) {
	obj, ok := keysAre(v, "op", "path", "value")
	if !ok {
		return "", "", nil, false
	}
	op, okOp := obj["op"].(string)
	path, okPath := obj["path"].(string)
	return op, path, obj["value"], okOp && okPath
}

// ops reads a JSON patch of n operations.
func ops(body any, n int) ([]any, bool) {
	arr, ok := body.([]any)
	return arr, ok && len(arr) == n
}

// is reports whether operation v is op at path with a value of typ.
func is(v any, op, path, typ string) bool {
	o, p, value, ok := patchOp(v)
	return ok && o == op && p == path && hasType(value, typ)
}

// addsFields is the UI's addsFields without removals: a JSON patch of adds,
// each on one of fields (path to JSON type), no path twice.
func addsFields(fields map[string]string) func(any) bool {
	return func(body any) bool {
		arr, ok := body.([]any)
		if !ok || len(arr) == 0 || len(arr) > len(fields) {
			return false
		}
		seen := map[string]bool{}
		for _, v := range arr {
			op, path, value, ok := patchOp(v)
			typ, known := fields[path]
			if !ok || op != opAdd || !known || seen[path] || !hasType(value, typ) {
				return false
			}
			seen[path] = true
		}
		return true
	}
}

// mergedAnnotations reads a merge patch that sets annotations and nothing
// else: exactly keys.
func mergedAnnotations(body any, keys ...string) (map[string]any, bool) {
	obj, ok := keysAre(body, fieldMetadata)
	if !ok {
		return nil, false
	}
	meta, ok := keysAre(obj[fieldMetadata], fieldAnnotations)
	if !ok {
		return nil, false
	}
	return keysAre(meta[fieldAnnotations], keys...)
}

// setsAnnotation is the UI's setsAnnotation: a merge patch setting exactly
// annotation key to a string.
func setsAnnotation(key string) func(any) bool {
	return func(body any) bool {
		annotations, ok := mergedAnnotations(body, key)
		return ok && hasType(annotations[key], jsonString)
	}
}

// setsAnnotationBy is a merge patch setting exactly annotation key and its
// companion naming the writer, both strings, the companion not empty.
func setsAnnotationBy(key string) func(any) bool {
	by := ChatAnnotationBy(key)
	return func(body any) bool {
		annotations, ok := mergedAnnotations(body, key, by)
		who, _ := annotations[by].(string)
		return ok && hasType(annotations[key], jsonString) && who != ""
	}
}

// createsBrief is a new Brief with a generated name or a name, and an idea
// only.
func createsBrief(body any) bool {
	obj, ok := keysAre(body, "apiVersion", "kind", fieldMetadata, "spec")
	if !ok || obj["apiVersion"] != ChatAPIVersion || obj["kind"] != "Brief" {
		return false
	}
	meta, okName := keysAre(obj[fieldMetadata], "name")
	if !okName {
		meta, ok = keysAre(obj[fieldMetadata], "generateName")
		if !ok || !hasType(meta["generateName"], jsonString) {
			return false
		}
	} else if !hasType(meta["name"], jsonString) {
		return false
	}
	spec, ok := keysAre(obj["spec"], "idea")
	return ok && hasType(spec["idea"], jsonString)
}

// appendsPrompt is one of the three appends addPrompt sends: an add at the
// list's end; a test of the resourceVersion and an add of the list; or a
// test of the list, its replacement and a raise of promptsRetired.
func appendsPrompt(body any) bool {
	if arr, ok := ops(body, 1); ok {
		return is(arr[0], opAdd, "/spec/prompts/-", jsonString)
	}
	if arr, ok := ops(body, 2); ok {
		_, _, rv, _ := patchOp(arr[0])
		return is(arr[0], opTest, "/metadata/resourceVersion", jsonString) && rv != "" &&
			is(arr[1], opAdd, pathPrompts, "strings")
	}
	if arr, ok := ops(body, 3); ok {
		return is(arr[0], opTest, pathPrompts, "strings") && is(arr[1], opReplace, pathPrompts, "strings") &&
			is(arr[2], opAdd, "/spec/promptsRetired", "integer")
	}
	return false
}

// chatDeployPath is a path in one of spec.deploy's first hundred entries:
// the entry, or one of its fields.
var chatDeployPath = regexp.MustCompile(`^/spec/deploy/(0|[1-9][0-9]?)(?:/([a-z]+))?$`)

// approvesDeploy is the UI's approvesDeploy: a test that entry N names a
// target, a test of its approval as read or of the whole entry, and an add
// of approved, the same N throughout.
func approvesDeploy(body any) bool {
	arr, ok := ops(body, 3)
	if !ok {
		return false
	}
	at := func(v any, op, field string, valid func(any) bool) (string, bool) {
		o, p, value, ok := patchOp(v)
		if !ok || o != op || !valid(value) {
			return "", false
		}
		m := chatDeployPath.FindStringSubmatch(p)
		if m == nil || m[2] != field {
			return "", false
		}
		return m[1], true
	}
	str := func(v any) bool { return hasType(v, jsonString) }
	entry := func(v any) bool {
		obj, ok := v.(map[string]any)
		if !ok || !hasType(obj[fieldTarget], jsonString) {
			return false
		}
		for k, f := range obj {
			if !slices.Contains([]string{fieldTarget, "after", "version"}, k) || !hasType(f, jsonString) {
				return false
			}
		}
		return true
	}
	n, ok := at(arr[0], opTest, fieldTarget, str)
	if !ok {
		return false
	}
	if m, ok := at(arr[2], opAdd, "approved", str); !ok || m != n {
		return false
	}
	if m, ok := at(arr[1], opTest, "approved", str); ok && m == n {
		return true
	}
	m, ok := at(arr[1], opTest, "", entry)
	return ok && m == n
}
