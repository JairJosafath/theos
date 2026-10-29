/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// A Brief's requests (docs/plans/done/brief-requests.md): the open issues of each
// spec.releases repository that takes them, opened by a person the entry
// allows, kept in status.requests. A stranger's issue is never kept, nor
// commented on.

// IssueTrackers builds the clients of the GitHub repositories a Brief takes
// requests from.
type IssueTrackers interface {
	GitHubIssues(cfg ports.GitHubConfig) (ports.IssueTracker, error)
	// GitHubToken is a GitHub Secret's token, or its GitHub App's
	// installation token, as the GitHub adapter's Client.GitHubToken mints it.
	GitHubToken(ctx context.Context, apiURL, repository string, secret map[string][]byte) (string, error)
}

const (
	// reasonRequestsReading is the Requests condition's reason while every
	// repository is read.
	reasonRequestsReading = "Reading"
	// eventRequests is the Warning Event of a Requests condition gone False.
	eventRequests = "Requests"
)

// requestsRead is what the Brief last read of one repository's issues. It
// lives in memory: a restart reads every repository again, once.
type requestsRead struct {
	// entry is the releases entry's mode, authors, teams and Secret it was
	// read for; another one reads again at once.
	entry  string
	readAt time.Time
	err    error
	etag   string
	open   []ports.GitHubIssue
	// members are the logins found in, or not in, the entry's teams while
	// the list is the same; unsure the ones that couldn't be read, with why.
	members    map[string]bool
	unsure     []string
	membersErr error
}

// requestsMode is a releases entry's requests mode, Off when it has none.
func requestsMode(entry *c4v1beta1.BriefRelease) string {
	if entry.Requests == nil || entry.Requests.Mode == "" {
		return model.RequestsOff
	}
	return entry.Requests.Mode
}

// requestsFailure is why a repository's requests couldn't be read or
// answered: the Requests condition's reason and message.
type requestsFailure struct {
	reason, message string
}

// syncRequests reads the issues of each releases repository that takes
// requests, at most every suggestions recheck unless its entry changed,
// keeps the requests in status.requests, comments on and closes the declined
// ones, and sets the Requests condition. A repository that fails keeps the
// requests it had; nothing it does fails the reconcile. It reports whether a
// repository takes requests, to read again.
func (r *BriefReconciler) syncRequests(ctx context.Context, brief *c4v1beta1.Brief) bool {
	status := &brief.Status
	now := r.now()
	r.shipRequests(ctx, brief)
	known := modelRequests(status.Requests)
	var all []model.Request
	var reading []string
	var failure *requestsFailure
	read := map[string]bool{}
	settled := map[string][]int32{}
	for i := range brief.Spec.Releases {
		entry := &brief.Spec.Releases[i]
		repository := entry.GitHub.Repository
		if requestsMode(entry) == model.RequestsOff || read[repository] {
			continue
		}
		read[repository] = true
		reading = append(reading, repository)
		got, fail := r.readRepository(ctx, brief, entry, known, settled, now)
		if fail != nil && failure == nil {
			fail.message = repository + ": " + fail.message
			failure = fail
		}
		all = append(all, got...)
	}
	r.forgetReads(brief, read)
	for _, repository := range knownRepositories(known) {
		if !read[repository] {
			// No longer read: what a round took stays, the rest goes.
			all = append(all, model.ReadRequests(known, model.RequestsRead{Repository: repository,
				Mode: model.RequestsOff}, now)...)
		}
	}
	r.recordRequests(ctx, brief, all)
	dropRequestsNeedsYou(brief)
	recordSettled(brief, reading, settled)
	r.setRequestsCondition(ctx, brief, reading, failure)
	return len(reading) > 0
}

// readRepository reads one entry's repository and answers its declined
// requests. It returns the repository's requests: those known when it can't
// be read. A read puts the entry's settled answers in settled.
func (r *BriefReconciler) readRepository(ctx context.Context, brief *c4v1beta1.Brief,
	entry *c4v1beta1.BriefRelease, known []model.Request, settled map[string][]int32,
	now time.Time) ([]model.Request, *requestsFailure) {
	repository := entry.GitHub.Repository
	same := slices.DeleteFunc(slices.Clone(known), func(k model.Request) bool { return k.Repository != repository })
	secret := entry.GitHub.TokenSecretRef.Name
	token, err := r.requestsToken(ctx, brief.Namespace, secret, repository)
	var mint *ports.ReleaseError
	switch {
	case errors.As(err, &mint):
		return same, &requestsFailure{mint.Reason, mint.Error()}
	case err != nil:
		return same, &requestsFailure{model.PublishSecretUnreadable,
			fmt.Sprintf("Secret %s can't be read: %s", secret, err.Error())}
	case token == "":
		return same, &requestsFailure{model.PublishSecretMissing,
			fmt.Sprintf("Secret %s with the key token (or a GitHub App's %s) is missing", secret,
				strings.Join(model.GitHubAppKeys, ", "))}
	}
	tracker, err := r.Issues.GitHubIssues(ports.GitHubConfig{Repository: repository, Token: token})
	if err != nil {
		return same, &requestsFailure{ports.ReasonGitHubError, err.Error()}
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	var owed []int
	for _, req := range same {
		if r.owesPlanned(brief, req) || req.Stage == model.RequestReleased {
			owed = append(owed, req.Number)
		}
	}
	last := r.readIssues(gitCtx, brief, entry, tracker, owed, now)
	if last.err != nil {
		return same, &requestsFailure{ports.IssuesReason(last.err), last.err.Error()}
	}
	spec := entry.Requests
	in := model.RequestsRead{Repository: repository, Mode: spec.Mode, Authors: spec.Authors, Unsure: last.unsure,
		Accepted: spec.Accepted, Declined: spec.Declined}
	for login, member := range last.members {
		if member {
			in.Members = append(in.Members, login)
		}
	}
	for _, i := range last.open {
		in.Open = append(in.Open, model.RequestIssue{Number: i.Number, Author: i.Author, Title: i.Title, Body: i.Body,
			URL: i.URL})
	}
	got := model.ReadRequests(known, in, now)
	var failure *requestsFailure
	if last.membersErr != nil {
		failure = &requestsFailure{ports.IssuesReason(last.membersErr), last.membersErr.Error()}
	}
	got, err = r.declineRequests(gitCtx, brief, tracker, got)
	if err != nil {
		failure = &requestsFailure{ports.IssuesReason(err), err.Error()}
	}
	got, err = r.answerPlanned(gitCtx, brief, tracker, got)
	if err != nil && failure == nil {
		failure = &requestsFailure{ports.IssuesReason(err), err.Error()}
	}
	got, err = r.answerReleased(gitCtx, brief, tracker, got)
	if err != nil && failure == nil {
		failure = &requestsFailure{ports.IssuesReason(err), err.Error()}
	}
	settled[repository] = model.SettledAnswers(in, got)
	return got, failure
}

// readIssues returns what the Brief last read of the entry's repository,
// reading it again when the entry changed or the suggestions recheck has
// passed: the open issues, asked with the last ETag, and the teams of the
// authors not named in authors. owed are the requests the Brief has to
// answer on GitHub, which it reads again first.
func (r *BriefReconciler) readIssues(ctx context.Context, brief *c4v1beta1.Brief, entry *c4v1beta1.BriefRelease,
	tracker ports.IssueTracker, owed []int, now time.Time) *requestsRead {
	spec := entry.Requests
	key := requestsKey(brief, entry.GitHub.Repository)
	fingerprint := fmt.Sprintf("%s %v %v %s", spec.Mode, spec.Authors, spec.Teams, entry.GitHub.TokenSecretRef.Name)
	r.requestsMu.Lock()
	last := r.requestsReads[key]
	r.requestsMu.Unlock()
	// A request about to be declined or answered is read again first, with
	// the ETag, so one its author closed meanwhile is dropped rather than
	// answered.
	if last != nil && last.entry == fingerprint && now.Sub(last.readAt) < r.suggestionsRecheck() &&
		!slices.ContainsFunc(last.open, func(i ports.GitHubIssue) bool {
			return slices.Contains(spec.Declined, int32(i.Number)) || slices.Contains(owed, i.Number)
		}) {
		return last
	}
	next := &requestsRead{entry: fingerprint, readAt: now}
	etag := ""
	if last != nil && last.entry == fingerprint && last.err == nil {
		etag = last.etag
	}
	list, err := tracker.ListOpenIssues(ctx, etag)
	switch {
	case err != nil:
		next.err = err
		if last != nil {
			// What it read before stays, for when the repository is back.
			next.etag, next.open = last.etag, last.open
		}
		logf.FromContext(ctx).Info("Could not read the issues of a releases repository", "repository",
			entry.GitHub.Repository, "error", err.Error())
	case list.NotModified:
		next.etag, next.open, next.members = list.ETag, last.open, last.members
	default:
		next.etag, next.open = list.ETag, list.Issues
	}
	if next.err == nil {
		r.readMembers(ctx, spec, tracker, next)
	}
	r.requestsMu.Lock()
	if r.requestsReads == nil {
		r.requestsReads = map[string]*requestsRead{}
	}
	r.requestsReads[key] = next
	r.requestsMu.Unlock()
	return next
}

// readMembers finds which authors of the open issues, not named in authors,
// are members of one of the entry's teams. An author already found keeps
// the answer while the list is the same.
func (r *BriefReconciler) readMembers(ctx context.Context, spec *c4v1beta1.BriefRequests,
	tracker ports.IssueTracker, read *requestsRead) {
	if len(spec.Teams) == 0 {
		read.members = nil
		return
	}
	members := map[string]bool{}
	for _, issue := range read.open {
		login := strings.ToLower(issue.Author)
		if _, done := members[login]; done || login == "" ||
			slices.ContainsFunc(spec.Authors, func(a string) bool { return strings.EqualFold(a, login) }) {
			continue
		}
		if member, found := read.members[login]; found {
			members[login] = member
			continue
		}
		member, err := inTeams(ctx, tracker, spec.Teams, login)
		if err != nil {
			read.unsure = append(read.unsure, login)
			if read.membersErr == nil {
				read.membersErr = err
			}
			continue
		}
		members[login] = member
	}
	read.members = members
}

// inTeams reports whether login is a member of one of teams.
func inTeams(ctx context.Context, tracker ports.IssueTracker, teams []string, login string) (bool, error) {
	for _, team := range teams {
		member, err := tracker.TeamMember(ctx, team, login)
		if err != nil {
			return false, err
		}
		if member {
			return true, nil
		}
	}
	return false, nil
}

// declineRequests says on each declined request that the app's owners didn't
// take it, once, and closes it as not planned; a closed one leaves the list.
// It goes on past a request it fails, and returns the first failure.
func (r *BriefReconciler) declineRequests(ctx context.Context, brief *c4v1beta1.Brief,
	tracker ports.IssueTracker, requests []model.Request) ([]model.Request, error) {
	var first error
	out := requests[:0:0]
	for _, req := range requests {
		if req.Stage != model.RequestDeclined {
			out = append(out, req)
			continue
		}
		err := declineRequest(ctx, tracker, brief.Name, req.Number)
		switch {
		case ports.IssuesReason(err) == ports.ReasonIssueNotFound:
			// Deleted or moved on GitHub: nothing to say.
		case err != nil:
			if first == nil {
				first = err
			}
			out = append(out, req)
			continue
		default:
			logf.FromContext(ctx).Info("Declined a request", "request", model.RequestName(req.Repository, req.Number))
		}
		r.forgetIssue(brief, req.Repository, req.Number)
	}
	return out, first
}

// declineRequest comments on issue number unless the Brief did before, then
// closes it as not planned.
func declineRequest(ctx context.Context, tracker ports.IssueTracker, brief string, number int) error {
	said, err := tracker.FindComment(ctx, number, model.RequestMarker(brief, model.CommentDeclined))
	if err != nil {
		return err
	}
	if said == nil {
		if _, err := tracker.Comment(ctx, number, model.DeclinedComment(brief)); err != nil {
			return err
		}
	}
	return tracker.CloseIssue(ctx, number, ports.CloseNotPlanned)
}

// forgetIssue takes an issue the Brief closed out of what it last read, so it
// isn't answered again before the next read.
func (r *BriefReconciler) forgetIssue(brief *c4v1beta1.Brief, repository string, number int) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	if last := r.requestsReads[requestsKey(brief, repository)]; last != nil {
		last.open = slices.DeleteFunc(slices.Clone(last.open), func(i ports.GitHubIssue) bool {
			return i.Number == number
		})
	}
}

// forgetReads drops what the Brief read of repositories it no longer reads,
// and when it last looked whether their requests shipped.
func (r *BriefReconciler) forgetReads(brief *c4v1beta1.Brief, read map[string]bool) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	prefix := requestsKey(brief, "")
	for key := range r.requestsReads {
		if repository, ok := strings.CutPrefix(key, prefix); ok && !read[repository] {
			delete(r.requestsReads, key)
		}
	}
	for key := range r.requestsShip {
		rest, ok := strings.CutPrefix(key, prefix)
		if repository, _, _ := strings.Cut(rest, "#"); ok && !read[repository] {
			delete(r.requestsShip, key)
		}
	}
}

func requestsKey(brief *c4v1beta1.Brief, repository string) string {
	return brief.Namespace + "/" + brief.Name + " " + repository
}

// requestsToken reads the token from the Secret named name, live, so a
// Secret created later is used on the next read, or mints its GitHub App's
// for repository alone; "" when there is none or it holds neither whole.
func (r *BriefReconciler) requestsToken(ctx context.Context, ns, name, repository string) (string, error) {
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if _, _, ok := model.GitHubCredential(secret.Data); !ok {
		return "", nil
	}
	return r.Issues.GitHubToken(ctx, "", repository, secret.Data)
}

// recordRequests sets status.requests to all, oldest first, keeping what a
// round recorded of the requests it took.
func (r *BriefReconciler) recordRequests(ctx context.Context, brief *c4v1beta1.Brief, all []model.Request) {
	status := &brief.Status
	before := map[string]c4v1beta1.BriefRequest{}
	for _, s := range status.Requests {
		before[model.RequestName(s.Repository, int(s.Number))] = s
	}
	listed, count := model.ListRequests(all)
	requests := make([]c4v1beta1.BriefRequest, 0, len(listed))
	for _, req := range listed {
		name := model.RequestName(req.Repository, req.Number)
		prev, seen := before[name]
		if seen && req.Round > 0 {
			requests = append(requests, prev)
			continue
		}
		if !seen || prev.Stage != req.Stage {
			logf.FromContext(ctx).Info("A request changed stage", "request", name, "stage", req.Stage)
		}
		since := metav1.NewTime(req.Since)
		requests = append(requests, c4v1beta1.BriefRequest{Repository: req.Repository, Number: int32(req.Number),
			Author: req.Author, Title: req.Title, Body: req.Body, URL: req.URL, Stage: req.Stage,
			Round: int32(req.Round), Since: &since})
	}
	status.Requests = nil
	if len(requests) > 0 {
		status.Requests = requests
	}
	status.RequestsWaiting = int32(count)
}

// recordSettled keeps status.requestsSettled: each repository read now gets
// what its read found, one that couldn't be read keeps what it had, and one
// no longer read goes.
func recordSettled(brief *c4v1beta1.Brief, reading []string, settled map[string][]int32) {
	before := map[string][]int32{}
	for _, s := range brief.Status.RequestsSettled {
		before[s.Repository] = s.Numbers
	}
	var out []c4v1beta1.BriefRequestsSettled
	for _, repository := range reading {
		numbers, read := settled[repository]
		if !read {
			numbers = before[repository]
		}
		if len(numbers) > 0 {
			out = append(out, c4v1beta1.BriefRequestsSettled{Repository: repository, Numbers: numbers})
		}
	}
	brief.Status.RequestsSettled = out
}

// setRequestsCondition says whether the repositories that take requests are
// read, with a Warning Event when it goes False for a new reason. With none,
// the condition goes.
func (r *BriefReconciler) setRequestsCondition(ctx context.Context, brief *c4v1beta1.Brief, reading []string,
	failure *requestsFailure) {
	status := &brief.Status
	if len(reading) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionRequests)
		return
	}
	if failure == nil {
		setCondition(&status.Conditions, brief.Generation, c4v1beta1.ConditionRequests, metav1.ConditionTrue,
			reasonRequestsReading, "Reading the issues of "+strings.Join(reading, ", "))
		return
	}
	prev := meta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionRequests)
	message := model.Clip(failure.message, 32768)
	if prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != failure.reason {
		logf.FromContext(ctx).Info("Could not read or answer requests", "reason", failure.reason, "message", message)
		r.event(brief, corev1.EventTypeWarning, eventRequests, "ReadRequests", failure.reason+": "+message)
	}
	setCondition(&status.Conditions, brief.Generation, c4v1beta1.ConditionRequests, metav1.ConditionFalse,
		failure.reason, message)
}

// modelRequests are the Brief's requests as its status keeps them.
func modelRequests(requests []c4v1beta1.BriefRequest) []model.Request {
	out := make([]model.Request, 0, len(requests))
	for _, s := range requests {
		req := model.Request{Repository: s.Repository, Number: int(s.Number), Author: s.Author, Title: s.Title,
			Body: s.Body, URL: s.URL, Stage: s.Stage, Round: int(s.Round), Issues: s.Issues, Release: s.Release,
			Why: s.Why}
		if s.Since != nil {
			req.Since = s.Since.Time
		}
		for _, sh := range s.Shipped {
			req.Shipped = append(req.Shipped, model.Shipped{Tag: sh.Tag, URL: sh.URL})
		}
		out = append(out, req)
	}
	return out
}

// knownRepositories are the repositories of known requests, sorted.
func knownRepositories(known []model.Request) []string {
	var out []string
	for _, k := range known {
		if !slices.Contains(out, k.Repository) {
			out = append(out, k.Repository)
		}
	}
	slices.Sort(out)
	return out
}

// dropRequestsNeedsYou takes out of status.needsYou the requests no longer
// waiting in status.requests, so the two never disagree in a status the
// reconcile writes before it tends the Brief: a round's start writes the
// status as soon as it has begun, and a step that fails leaves what it had
// (#1930). The count and the NeedsYou condition follow; the next tend lists
// anew what the drop made room for.
func dropRequestsNeedsYou(brief *c4v1beta1.Brief) {
	status := &brief.Status
	waiting := waitingRequests(brief)
	kept := make([]c4v1beta1.BriefNeedsYou, 0, len(status.NeedsYou))
	for _, n := range status.NeedsYou {
		if n.Kind == model.AttentionRequest && !slices.ContainsFunc(waiting, func(w model.NeedsYou) bool {
			return w.Name == n.Name
		}) {
			continue
		}
		kept = append(kept, n)
	}
	dropped := len(status.NeedsYou) - len(kept)
	if dropped == 0 {
		return
	}
	status.NeedsYou = nil
	if len(kept) > 0 {
		status.NeedsYou = kept
	}
	listed := make([]model.NeedsYou, 0, len(kept))
	for _, n := range kept {
		listed = append(listed, model.NeedsYou{Kind: n.Kind, Name: n.Name, Question: n.Question, Recommend: n.Recommend})
	}
	setNeedsYouWaiting(brief, listed, max(int(status.NeedsYouWaiting)-dropped, len(kept)))
}

// waitingRequests are the status.needsYou items of the requests waiting in
// Ask.
func waitingRequests(brief *c4v1beta1.Brief) []model.NeedsYou {
	var out []model.NeedsYou
	for _, req := range modelRequests(brief.Status.Requests) {
		if req.Stage == model.RequestWaiting {
			out = append(out, model.RequestNeedsYou(req))
		}
	}
	return out
}
