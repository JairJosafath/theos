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
	"fmt"
	"slices"
	"strings"
)

// A person's word from chat reaches the operator as Task annotations
// (docs/plans/done/chat-matrix.md, "Specs approved from chat"): the head commit a
// person approves (ChatAnnotationApproveSpec), the changes they ask of it
// (ChatAnnotationSpecChanges) and their answer to a parked issue's question
// (ChatAnnotationAnswer), each with its companion naming who wrote it
// (ChatAnnotationBy), which the admission policy holds to the writer.

// ChatWord is one of the chat annotations as a Task holds it: its value and
// its companion, the Kubernetes user who wrote it.
type ChatWord struct {
	Value, By string
}

// ChatWordOf reads annotation key and its companion from annotations.
func ChatWordOf(annotations map[string]string, key string) ChatWord {
	return ChatWord{Value: annotations[key], By: annotations[ChatAnnotationBy(key)]}
}

// shortCommit is a commit's first seven characters, as people read it.
func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// ChatSpecVerdict is what people said of a spec's head commit from chat: an
// approval of head (ChatAnnotationApproveSpec), or changes asked of it
// (ChatAnnotationSpecChanges), each by the Kubernetes user its companion
// names. A word about another commit, or one no companion names, counts for
// nothing; Ignored says why, for the Task's condition.
func ChatSpecVerdict(approve, changes ChatWord, head string) (v PersonVerdict, ignored []string) {
	if approve.Value != "" {
		switch {
		case approve.By == "":
			ignored = append(ignored, fmt.Sprintf("the approval from chat (%s) names nobody in %s, so it doesn't count",
				ChatAnnotationApproveSpec, ChatAnnotationBy(ChatAnnotationApproveSpec)))
		case approve.Value != head:
			ignored = append(ignored, fmt.Sprintf("%s's approval from chat is of commit %s, not the head %s, so it "+
				"doesn't count", approve.By, shortCommit(approve.Value), shortCommit(head)))
		default:
			v.ApprovedBy, v.ApprovedInChat = approve.By, true
		}
	}
	if changes.Value != "" {
		commit, text, ok := ParseChatSpecChanges(changes.Value)
		switch {
		case changes.By == "":
			ignored = append(ignored, fmt.Sprintf("the changes asked from chat (%s) name nobody in %s, so they don't "+
				"count", ChatAnnotationSpecChanges, ChatAnnotationBy(ChatAnnotationSpecChanges)))
		case !ok:
			ignored = append(ignored, fmt.Sprintf("%s's changes from chat aren't a commit and the changes, so they "+
				"don't count", changes.By))
		case commit != head:
			ignored = append(ignored, fmt.Sprintf("%s's changes from chat are asked of commit %s, not the head %s, so "+
				"they don't count", changes.By, shortCommit(commit), shortCommit(head)))
		default:
			v.ChangesBy, v.Changes, v.ChangesInChat = changes.By, strings.TrimSpace(text), true
		}
	}
	if v.ChangesBy != "" {
		v.ApprovedBy, v.ApprovedInChat = "", false
	}
	return v, ignored
}

// CombineVerdicts is the people's verdict on a spec from the Git host and
// from chat: a request for changes from either outweighs any approval, the
// Git host's word first.
func CombineVerdicts(host, chat PersonVerdict) PersonVerdict {
	switch {
	case host.ChangesBy != "":
		return PersonVerdict{ChangesBy: host.ChangesBy, Changes: host.Changes}
	case chat.ChangesBy != "":
		return PersonVerdict{ChangesBy: chat.ChangesBy, Changes: chat.Changes, ChangesInChat: true}
	case host.ApprovedBy != "":
		return PersonVerdict{ApprovedBy: host.ApprovedBy}
	}
	return chat
}

// ChatAnswerComment is the comment a person's answer from chat makes on the
// parked issue: who answered, then their words as they wrote them.
func ChatAnswerComment(by, answer string) string {
	return by + " answered in chat:\n\n" + strings.TrimSpace(answer)
}

// ChatAnswerActions is what the operator does with a person's answer from
// chat to a parked issue's question.
type ChatAnswerActions struct {
	// Ignore says why the answer is left as it is: the Task parked no
	// question, the answer names nobody, or one was taken already. Nothing is
	// written and nothing recorded.
	Ignore string
	// Comment goes on the issue, with the answer.
	Comment string
	// Relabel puts the issue back to status/proposed, from status/needs-human,
	// so the Crew triages it again.
	Relabel bool
	// Answered says what was done, for the Task's status.
	Answered string
}

// PlanChatAnswer decides what the operator does with answer, the
// ChatAnnotationAnswer of a Triage Task whose triager decided decision,
// and which answeredBy records as taken already (empty for none), for the
// issue as it reads now: the answer goes to the issue as a comment, and an
// issue still status/needs-human goes back to status/proposed. A closed
// issue gets nothing, and one a person labelled otherwise meanwhile keeps
// its label.
func PlanChatAnswer(decision, answeredBy string, answer ChatWord, issue TriageIssue) ChatAnswerActions {
	switch {
	case answer.Value == "" || answeredBy != "":
		return ChatAnswerActions{}
	case decision != TriagePark:
		return ChatAnswerActions{Ignore: fmt.Sprintf("the answer from chat is to no question: the triager decided %s, "+
			"not park", orUnknown(decision))}
	case answer.By == "":
		return ChatAnswerActions{Ignore: fmt.Sprintf("the answer from chat names nobody in %s, so it doesn't count",
			ChatAnnotationBy(ChatAnnotationAnswer))}
	case issue.Closed:
		return ChatAnswerActions{Answered: fmt.Sprintf("Issue #%d was closed; %s's answer was not posted",
			issue.Number, answer.By)}
	}
	out := ChatAnswerActions{Comment: ChatAnswerComment(answer.By, answer.Value)}
	if slices.Contains(issue.Labels, LabelNeedsHuman) {
		out.Relabel = true
		out.Answered = fmt.Sprintf("Commented %s's answer on issue #%d and labelled it %s to be triaged again",
			answer.By, issue.Number, LabelProposed)
		return out
	}
	now := "no status label"
	if i := slices.IndexFunc(issue.Labels, func(l string) bool { return strings.HasPrefix(l, "status/") }); i >= 0 {
		now = issue.Labels[i]
	}
	out.Answered = fmt.Sprintf("Commented %s's answer on issue #%d, which keeps %s: a person moved it meanwhile",
		answer.By, issue.Number, now)
	return out
}

// ChatPerson is one of a Chat's people: their Matrix user ID and the
// Kubernetes user and groups their commands are made as.
type ChatPerson struct {
	MatrixID string
	User     string
	Groups   []string
}

// ChatPersonFor is the one of people whose Matrix ID is sender, false for a
// stranger. Matrix IDs compare exactly, as the homeserver sends them; a
// person who names no Kubernetes user is nobody's.
func ChatPersonFor(people []ChatPerson, sender string) (ChatPerson, bool) {
	i := slices.IndexFunc(people, func(p ChatPerson) bool { return p.MatrixID == sender })
	if i < 0 || sender == "" || people[i].User == "" {
		return ChatPerson{}, false
	}
	return people[i], true
}

// ChatStrangerRefusal is the one line a sender outside a Chat's people gets
// for a command: nothing is run.
func ChatStrangerRefusal(sender, chat string) string {
	return fmt.Sprintf("%s is not one of Chat %s's people, so nothing was done; whoever runs the project adds you "+
		"to its spec.people", sender, chat)
}

// ChatRefused is what theos-chat posts when the API server refuses a
// person's request: its words, as the person's RBAC or the CRDs decided.
func ChatRefused(user string, code int, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "no reason given"
	}
	return fmt.Sprintf("The cluster refused it for %s (%d): %s", user, code, message)
}

// ChatDone is what theos-chat posts when the API server took a person's
// request: what was done, as whom, and the object it made, if any.
func ChatDone(cmd ChatCommand, item ChatItem, user, made string) string {
	var what string
	switch cmd.Kind {
	case ChatCommandBrief:
		what = "made Brief " + made
	case ChatCommandPrompt:
		what = "added the prompt to Brief " + cmd.Brief
	case ChatCommandPause:
		what = "paused Brief " + cmd.Brief
	case ChatCommandResume:
		what = "resumed Brief " + cmd.Brief
	case ChatCommandAnswer:
		what = "sent your answer; it goes to the issue, which is then triaged again"
	case ChatCommandApproveSpec:
		what = fmt.Sprintf("approved the spec at %s; the operator merges it", shortCommit(item.Commit))
		if item.Event == ChatEventChangeWaiting {
			what = fmt.Sprintf("approved the change at %s; the operator merges it", shortCommit(item.Commit))
		}
	case ChatCommandChanges:
		to := " to the spec"
		if item.Event == ChatEventChangeWaiting {
			to = ""
		}
		what = fmt.Sprintf("asked for changes%s at %s; the next round takes them", to, shortCommit(item.Commit))
	case ChatCommandApproveDeploy:
		what = fmt.Sprintf("let %s into %s", cmd.Version, item.Target)
	case ChatCommandRetry:
		what = "asked Container " + item.Name + " to scaffold again"
	default:
		what = "done"
	}
	return fmt.Sprintf("Done as %s: %s.", user, what)
}
