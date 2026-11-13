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
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Brief's requests (docs/plans/done/brief-requests.md): the open issues of each
// spec.releases repository that takes them, opened by a person the entry
// allows, kept in status.requests. A stranger's issue is never kept, nor
// commented on. Each spec.requestSources entry is read the same way
// (docs/plans/requests-and-chat.md, step 10): its tracker lists the open
// items that carry its label, and each is a request, whoever made it.

const (
	// reasonRequestsReading is the Requests condition's reason while every
	// repository is read.
	reasonRequestsReading = "Reading"
	// eventRequests is the Warning Event of a Requests condition gone False.
	eventRequests = "Requests"
)

// requestsRead is what the Brief last read of one repository's issues, or
// one source's items. It lives in memory: a restart reads every one again,
// once.
type requestsRead struct {
	// entry is the entry's mode, authors, teams, Secret and tracker it was
	// read for; another one reads again at once.
	entry  string
	readAt time.Time
	err    error
	etag   string
	open   []ports.TrackerIssue
	// members are the logins found in, or not in, the entry's teams while
	// the list is the same; unsure the ones that couldn't be read, with why.
	members    map[string]bool
	unsure     []string
	membersErr error
	// checked are the items the Brief closed, or read on their own, by
	// number, kept across reads while the list shows them (#161).
	checked map[int]itemCheck
}

// itemCheck is what the Brief knows of one listed item beyond the list: that
// it closed it, or what reading the item on its own said, and when. A source's
// list is a search, which can lag behind its items for minutes and longer (a
// search index does): an item the Brief closed may be listed still, and
// would be read as a new request and planned twice. err is why the read
// failed, or why it wasn't made (#245): the item is left out until the
// recheck, and read again then.
type itemCheck struct {
	open bool
	err  error
	at   time.Time
}

// requestsMode is a releases entry's requests mode, Off when it has none.
func requestsMode(entry *c4v1beta1.BriefRelease) string {
	if entry.Requests == nil || entry.Requests.Mode == "" {
		return model.RequestsOff
	}
	return entry.Requests.Mode
}

// requestOrigin is where a Brief reads requests: a releases entry's GitHub
// repository, or a spec.requestSources entry's tracker.
type requestOrigin struct {
	// repository is the GitHub repository; source the request source's
	// name. One of the two is set.
	repository, source string
	mode               string
	// authors and teams let a GitHub issue's author ask; a source has none,
	// since its label is the door.
	authors, teams     []string
	accepted, declined []int32
	// secret names the Secret the tracker is read with.
	secret  string
	tracker ports.TrackerSource
}

// originID names a repository or a source in what the Brief keeps in
// memory: a repository is owner/name, a source source:name, which holds no
// '/' and never equals one.
func originID(repository, source string) string {
	if source != "" {
		return "source:" + source
	}
	return repository
}

func (o requestOrigin) id() string { return originID(o.repository, o.source) }

// label names the origin in the Requests condition's messages.
func (o requestOrigin) label() string {
	if o.source != "" {
		return "request source " + o.source
	}
	return o.repository
}

// owns reports whether req is a request of the origin.
func (o requestOrigin) owns(req model.Request) bool {
	return req.Repository == o.repository && req.Source == o.source
}

// requestOrigins are the repositories and sources the Brief reads requests
// from: each releases repository whose entry's mode isn't Off, once, then
// each request source whose mode isn't Off. An entry in Off reads nothing.
func requestOrigins(brief *c4v1beta1.Brief) []requestOrigin {
	var out []requestOrigin
	for i := range brief.Spec.Releases {
		entry := &brief.Spec.Releases[i]
		repository := entry.GitHub.Repository
		if requestsMode(entry) == model.RequestsOff ||
			slices.ContainsFunc(out, func(o requestOrigin) bool { return o.repository == repository }) {
			continue
		}
		spec := entry.Requests
		out = append(out, requestOrigin{repository: repository, mode: spec.Mode, authors: spec.Authors,
			teams: spec.Teams, accepted: spec.Accepted, declined: spec.Declined,
			secret:  entry.GitHub.TokenSecretRef.Name,
			tracker: ports.TrackerSource{Tracker: model.TrackerGitHub, Project: repository}})
	}
	for i := range brief.Spec.RequestSources {
		src := &brief.Spec.RequestSources[i]
		mode := src.Mode
		if mode == "" {
			mode = model.RequestsAsk
		}
		if mode == model.RequestsOff {
			continue
		}
		out = append(out, requestOrigin{source: src.Name, mode: mode, accepted: src.Accepted,
			declined: src.Declined, secret: src.CredentialsSecretRef.Name, tracker: sourceTracker(src)})
	}
	return out
}

// sourceTracker is where a request source's items are, as its tracker's
// adapter takes it.
func sourceTracker(src *c4v1beta1.BriefRequestSource) ports.TrackerSource {
	switch {
	case src.Linear != nil:
		return ports.TrackerSource{Tracker: model.TrackerLinear, Project: src.Linear.Team, Label: src.Linear.Label}
	case src.Jira != nil:
		j := src.Jira
		source := ports.TrackerSource{Tracker: model.TrackerJira, Project: j.Project, Label: j.Label, Site: j.Site,
			Instance: j.CloudID}
		if j.Close != nil {
			source.Close = map[ports.CloseReason]ports.CloseOutcome{}
			for reason, o := range map[ports.CloseReason]*c4v1beta1.BriefJiraOutcome{
				ports.CloseCompleted: j.Close.Completed, ports.CloseNotPlanned: j.Close.NotPlanned} {
				if o != nil {
					source.Close[reason] = ports.CloseOutcome{Status: o.Status, Resolution: o.Resolution}
				}
			}
		}
		return source
	}
	return ports.TrackerSource{}
}

// readsOrigin reports whether the Brief reads the repository or source of
// req: an entry names it with a mode other than Off.
func readsOrigin(brief *c4v1beta1.Brief, req model.Request) bool {
	return slices.ContainsFunc(requestOrigins(brief), func(o requestOrigin) bool { return o.owns(req) })
}

// requestsFailure is why a repository's requests couldn't be read or
// answered: the Requests condition's reason and message.
type requestsFailure struct {
	reason, message string
}

// syncRequests reads the issues of each releases repository that takes
// requests, and the items of each request source, at most every suggestions
// recheck unless its entry changed, keeps the requests in status.requests,
// comments on and closes the declined ones, and sets the Requests condition.
// A repository or source that fails keeps the requests it had; nothing it
// does fails the reconcile. It reports whether one takes requests, to read
// again.
func (r *BriefReconciler) syncRequests(ctx context.Context, brief *c4v1beta1.Brief) bool {
	status := &brief.Status
	now := r.now()
	r.shipRequests(ctx, brief)
	known := modelRequests(status.Requests)
	var all []model.Request
	reading := requestOrigins(brief)
	var failure *requestsFailure
	read := map[string]bool{}
	settled := map[string][]int32{}
	for _, origin := range reading {
		read[origin.id()] = true
		got, fail := r.readOrigin(ctx, brief, origin, known, settled, now)
		if fail != nil && failure == nil {
			fail.message = origin.label() + ": " + fail.message
			failure = fail
		}
		all = append(all, got...)
	}
	r.forgetReads(brief, read)
	for _, gone := range knownOrigins(known) {
		if !read[gone.id()] {
			// No longer read: what a round took stays, the rest goes. Nothing
			// is read of it, nor written on it.
			all = append(all, model.ReadRequests(known, model.RequestsRead{Repository: gone.repository,
				Source: gone.source, Mode: model.RequestsOff}, now)...)
		}
	}
	r.recordRequests(ctx, brief, all)
	dropRequestsNeedsYou(brief)
	recordSettled(brief, reading, settled)
	r.setRequestsCondition(ctx, brief, reading, failure)
	return len(reading) > 0
}

// readOrigin reads one repository's issues, or one source's items, and
// answers its requests on them. It returns the origin's requests: those known
// when it can't be read. A read puts the origin's settled answers in settled.
func (r *BriefReconciler) readOrigin(ctx context.Context, brief *c4v1beta1.Brief, origin requestOrigin,
	known []model.Request, settled map[string][]int32, now time.Time) ([]model.Request, *requestsFailure) {
	same := slices.DeleteFunc(slices.Clone(known), func(k model.Request) bool { return !origin.owns(k) })
	tracker, fail := r.requestsTracker(ctx, brief.Namespace, origin.secret, origin.tracker)
	if fail != nil {
		return same, fail
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	// A request whose close the workflow refused waits for the recheck
	// rather than forcing a read on every reconcile.
	var owed []int
	for _, req := range same {
		if (r.owesPlanned(brief, req) || req.Stage == model.RequestReleased) && r.heldClose(brief, req, now) == nil {
			owed = append(owed, req.Number)
		}
	}
	for _, n := range origin.declined {
		req := model.Request{Repository: origin.repository, Source: origin.source, Number: int(n)}
		if r.heldClose(brief, req, now) == nil {
			owed = append(owed, int(n))
		}
	}
	last := r.readIssues(gitCtx, brief, origin, tracker, owed, now)
	if last.err != nil {
		return same, &requestsFailure{ports.IssuesReason(last.err), last.err.Error()}
	}
	listed, listErr := r.listedItems(gitCtx, origin, tracker, last, same, now)
	in := model.RequestsRead{Repository: origin.repository, Source: origin.source, Mode: origin.mode,
		Authors: origin.authors, Unsure: last.unsure, Accepted: origin.accepted, Declined: origin.declined}
	for login, member := range last.members {
		if member {
			in.Members = append(in.Members, login)
		}
	}
	for _, i := range listed {
		in.Open = append(in.Open, model.RequestIssue{Number: i.Number, Key: i.Key, Author: i.Author, Title: i.Title,
			Body: i.Body, URL: i.URL})
	}
	got := model.ReadRequests(known, in, now)
	var failure *requestsFailure
	if last.membersErr != nil {
		failure = &requestsFailure{ports.IssuesReason(last.membersErr), last.membersErr.Error()}
	}
	if listErr != nil && failure == nil {
		failure = &requestsFailure{ports.IssuesReason(listErr), listErr.Error()}
	}
	got, err := r.declineRequests(gitCtx, brief, tracker, got)
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
	settled[origin.id()] = model.SettledAnswers(in, got)
	r.releaseHeld(brief, origin, got)
	return got, failure
}

// readIssues returns what the Brief last read of the origin's repository or
// source, reading it again when its entry changed or the suggestions recheck
// has passed: the open issues, asked with the last ETag, and the teams of the
// authors not named in authors. owed are the requests the Brief has to
// answer on the tracker, and the declined numbers, which it reads again
// first.
func (r *BriefReconciler) readIssues(ctx context.Context, brief *c4v1beta1.Brief, origin requestOrigin,
	tracker ports.IssueTracker, owed []int, now time.Time) *requestsRead {
	key := requestsKey(brief, origin.id())
	fingerprint := fmt.Sprintf("%s %v %v %s %+v", origin.mode, origin.authors, origin.teams, origin.secret,
		origin.tracker)
	r.requestsMu.Lock()
	last := r.requestsReads[key]
	r.requestsMu.Unlock()
	// A request about to be declined or answered is read again first, with
	// the ETag, so one its author closed meanwhile is dropped rather than
	// answered.
	if last != nil && last.entry == fingerprint && now.Sub(last.readAt) < r.suggestionsRecheck() &&
		!slices.ContainsFunc(last.open, func(i ports.TrackerIssue) bool { return slices.Contains(owed, i.Number) }) {
		return last
	}
	next := &requestsRead{entry: fingerprint, readAt: now}
	etag := ""
	if last != nil && last.entry == fingerprint {
		r.requestsMu.Lock()
		next.checked = maps.Clone(last.checked)
		r.requestsMu.Unlock()
		if last.err == nil {
			etag = last.etag
		}
	}
	list, err := tracker.ListOpenIssues(ctx, etag)
	switch {
	case err != nil:
		next.err = err
		if last != nil {
			// What it read before stays, for when the repository is back.
			next.etag, next.open = last.etag, last.open
		}
		logf.FromContext(ctx).Info("Could not read the issues of a releases repository or a request source",
			"repository", origin.repository, "source", origin.source, "error", err.Error())
	case list.NotModified:
		next.etag, next.open, next.members = list.ETag, last.open, last.members
	default:
		next.etag, next.open = list.ETag, list.Issues
		// An item the list no longer shows is done with; one listed again
		// later is read anew.
		maps.DeleteFunc(next.checked, func(n int, _ itemCheck) bool {
			return !slices.ContainsFunc(list.Issues, func(i ports.TrackerIssue) bool { return i.Number == n })
		})
	}
	if next.err == nil {
		r.readMembers(ctx, origin, tracker, next)
	}
	r.requestsMu.Lock()
	if r.requestsReads == nil {
		r.requestsReads = map[string]*requestsRead{}
	}
	r.requestsReads[key] = next
	r.requestsMu.Unlock()
	return next
}

// listedItems are the items of the origin's last read that may be requests
// (#161): each one a request is kept for, and each other one unless the
// Brief closed it or found it closed less than a suggestions recheck ago. A
// source's list is a search, which can lag behind its items, so a source's
// item no request is kept for is read on its own first where its tracker can
// (ports.IssueReader), and left out unless it is open and labelled: what the
// Brief closed is never asked again, whatever the search says, and after a
// restart too. What a read found is kept for a recheck, so an item is read
// once per recheck at most. A read that fails is kept so too (#245): its item
// is left out until the recheck, and the failure is returned on each pass
// until then. One the tracker refused as unavailable (a rate limit, an
// outage) ends the pass's reads: the items not read yet wait for the recheck
// with it.
func (r *BriefReconciler) listedItems(ctx context.Context, origin requestOrigin,
	tracker ports.IssueTracker, last *requestsRead, same []model.Request, now time.Time) ([]ports.TrackerIssue, error) {
	reader, reads := tracker.(ports.IssueReader)
	reads = reads && origin.source != ""
	var out []ports.TrackerIssue
	var first, unavailable error
	for _, i := range last.open {
		if slices.ContainsFunc(same, func(k model.Request) bool { return k.Number == i.Number }) {
			out = append(out, i)
			continue
		}
		r.requestsMu.Lock()
		check, checked := last.checked[i.Number]
		r.requestsMu.Unlock()
		switch {
		case checked && now.Sub(check.at) < r.suggestionsRecheck():
			if check.err != nil && first == nil {
				first = check.err
			}
			if check.open {
				out = append(out, i)
			}
			continue
		case !reads:
			out = append(out, i)
			continue
		case unavailable != nil:
			r.checkItem(last, i.Number, itemCheck{err: unavailable, at: now})
			continue
		}
		open, err := reader.IssueOpen(ctx, i.Number)
		if err != nil {
			if first == nil {
				first = err
			}
			if ports.IssuesReason(err) == ports.ReasonTrackerUnavailable {
				unavailable = err
			}
			r.checkItem(last, i.Number, itemCheck{err: err, at: now})
			continue
		}
		r.checkItem(last, i.Number, itemCheck{open: open, at: now})
		if open {
			out = append(out, i)
		}
	}
	return out, first
}

// checkItem keeps what the Brief knows of item number of read.
func (r *BriefReconciler) checkItem(read *requestsRead, number int, check itemCheck) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	if read.checked == nil {
		read.checked = map[int]itemCheck{}
	}
	read.checked[number] = check
}

// readMembers finds which authors of the open issues, not named in authors,
// are members of one of the entry's teams. An author already found keeps
// the answer while the list is the same.
func (r *BriefReconciler) readMembers(ctx context.Context, origin requestOrigin,
	tracker ports.IssueTracker, read *requestsRead) {
	if len(origin.teams) == 0 {
		read.members = nil
		return
	}
	members := map[string]bool{}
	for _, issue := range read.open {
		login := strings.ToLower(issue.Author)
		if _, done := members[login]; done || login == "" ||
			slices.ContainsFunc(origin.authors, func(a string) bool { return strings.EqualFold(a, login) }) {
			continue
		}
		if member, found := read.members[login]; found {
			members[login] = member
			continue
		}
		member, err := inTeams(ctx, tracker, origin.teams, login)
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

// inTeams reports whether login is a member of one of teams. Teams are an
// optional capability of a tracker (ports.TeamMembers): one without them
// can't say, which is a failure the Requests condition shows, never a
// stranger let in.
func inTeams(ctx context.Context, tracker ports.IssueTracker, teams []string, login string) (bool, error) {
	if len(teams) == 0 {
		return false, nil
	}
	members, ok := tracker.(ports.TeamMembers)
	if !ok {
		return false, &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: "the tracker has no teams whose members it can read"}
	}
	for _, team := range teams {
		member, err := members.TeamMember(ctx, team, login)
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
	now := r.now()
	for _, req := range requests {
		if req.Stage != model.RequestDeclined {
			out = append(out, req)
			continue
		}
		err := r.heldClose(brief, req, now)
		if err == nil {
			err = declineRequest(ctx, tracker, brief.Name, req)
			r.holdClose(brief, req, err, now)
		}
		switch {
		case ports.IssuesReason(err) == ports.ReasonIssueNotFound:
			// Deleted or moved on the tracker: nothing to say.
		case err != nil:
			if first == nil {
				first = err
			}
			out = append(out, req)
			continue
		default:
			logf.FromContext(ctx).Info("Declined a request", "request", req.Name())
		}
		r.forgetIssue(brief, req)
	}
	return out, first
}

// declineRequest comments on the request unless the Brief did before, then
// closes it as not planned.
func declineRequest(ctx context.Context, tracker ports.IssueTracker, brief string, req model.Request) error {
	said, err := tracker.FindComment(ctx, req.Number, model.RequestMarker(brief, model.CommentDeclined))
	if err != nil {
		return err
	}
	if said == nil {
		if _, err := tracker.Comment(ctx, req.Number, requestComment(req, model.DeclinedComment(brief))); err != nil {
			return err
		}
	}
	return tracker.CloseIssue(ctx, req.Number, ports.CloseNotPlanned)
}

// requestComment is a comment as req's tracker shows it: a source's tracker
// shows no hidden text, so there the markers are its last lines.
func requestComment(req model.Request, text string) string {
	if req.Source != "" {
		return model.MarkersLast(text)
	}
	return text
}

// forgetIssue takes a request the Brief closed out of what it last read, so
// it isn't answered again before the next read, and keeps that it closed it:
// a list that lags may show it again (#161), and it is no new request.
func (r *BriefReconciler) forgetIssue(brief *c4v1beta1.Brief, req model.Request) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	if last := r.requestsReads[requestsKey(brief, originID(req.Repository, req.Source))]; last != nil {
		last.open = slices.DeleteFunc(slices.Clone(last.open), func(i ports.TrackerIssue) bool {
			return i.Number == req.Number
		})
		if last.checked == nil {
			last.checked = map[int]itemCheck{}
		}
		last.checked[req.Number] = itemCheck{at: r.now()}
	}
}

// heldClose is a close of the tracker's workflow refused for a close of req
// (ports.ReasonCannotClose).
type heldClose struct {
	at  time.Time
	err error
}

func heldKey(brief *c4v1beta1.Brief, req model.Request) string {
	return requestsKey(brief, originID(req.Repository, req.Source)) + fmt.Sprintf("#%d", req.Number)
}

// heldClose is why the tracker's workflow refused to close req less than a
// suggestions recheck ago, nil when it didn't: a workflow that has no way to
// close it won't have one a second later, and the source isn't asked again
// on every reconcile until then.
func (r *BriefReconciler) heldClose(brief *c4v1beta1.Brief, req model.Request, now time.Time) error {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	held, ok := r.requestsHeld[heldKey(brief, req)]
	if !ok || now.Sub(held.at) >= r.suggestionsRecheck() {
		return nil
	}
	return held.err
}

// holdClose keeps err, what answering req ended in, when the tracker's
// workflow refused the close, and forgets a refusal before otherwise.
func (r *BriefReconciler) holdClose(brief *c4v1beta1.Brief, req model.Request, err error, now time.Time) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	key := heldKey(brief, req)
	if err == nil || ports.IssuesReason(err) != ports.ReasonCannotClose {
		delete(r.requestsHeld, key)
		return
	}
	if r.requestsHeld == nil {
		r.requestsHeld = map[string]heldClose{}
	}
	r.requestsHeld[key] = heldClose{at: now, err: err}
}

// releaseHeld forgets the held closes of the origin's requests that are no
// longer among requests, what the Brief keeps of it in status.requests
// (#245): a request whose label was taken off while its close was held
// leaves the list, and when it comes back its close is tried at once, not
// after what is left of the old refusal's recheck.
func (r *BriefReconciler) releaseHeld(brief *c4v1beta1.Brief, origin requestOrigin, requests []model.Request) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	if len(r.requestsHeld) == 0 {
		return
	}
	kept := map[string]bool{}
	for _, req := range requests {
		kept[heldKey(brief, req)] = true
	}
	prefix := requestsKey(brief, origin.id()) + "#"
	for key := range r.requestsHeld {
		if strings.HasPrefix(key, prefix) && !kept[key] {
			delete(r.requestsHeld, key)
		}
	}
}

// forgetReads drops what the Brief read of repositories and sources it no
// longer reads, and when it last looked whether their requests shipped. read
// holds their originIDs.
func (r *BriefReconciler) forgetReads(brief *c4v1beta1.Brief, read map[string]bool) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	prefix := requestsKey(brief, "")
	for key := range r.requestsReads {
		if id, ok := strings.CutPrefix(key, prefix); ok && !read[id] {
			delete(r.requestsReads, key)
		}
	}
	for key := range r.requestsShip {
		rest, ok := strings.CutPrefix(key, prefix)
		if id, _, _ := strings.Cut(rest, "#"); ok && !read[id] {
			delete(r.requestsShip, key)
		}
	}
	for key := range r.requestsHeld {
		rest, ok := strings.CutPrefix(key, prefix)
		if id, _, _ := strings.Cut(rest, "#"); ok && !read[id] {
			delete(r.requestsHeld, key)
		}
	}
}

// requestsKey keys what the Brief keeps in memory of a repository or source,
// by its originID.
func requestsKey(brief *c4v1beta1.Brief, id string) string {
	return brief.Namespace + "/" + brief.Name + " " + id
}

// requestsTracker builds the tracker of source with the data of the Secret
// named name, read live, so a Secret created later is used on the next read.
// Which keys the Secret holds, and minting a token from them, are the
// tracker's adapter's business: a Secret that is missing, or holds no whole
// credential, is passed on as it is, and the adapter says which keys it
// takes.
func (r *BriefReconciler) requestsTracker(ctx context.Context, ns, name string,
	source ports.TrackerSource) (ports.IssueTracker, *requestsFailure) {
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, &requestsFailure{model.PublishSecretUnreadable,
			fmt.Sprintf("Secret %s can't be read: %s", name, err.Error())}
	}
	tracker, err := r.Issues.Tracker(ctx, source, secret.Data)
	if missing, ok := errors.AsType[*ports.CredentialMissingError](err); ok {
		return nil, &requestsFailure{model.PublishSecretMissing,
			fmt.Sprintf("Secret %s with %s is missing", name, missing.Keys)}
	}
	if err != nil {
		// A credential refused while the tracker is built says why, as a
		// call that fails does; anything else is the tracker's error.
		reason := ports.ReasonTrackerError
		if failed, ok := errors.AsType[*ports.IssuesError](err); ok {
			reason = failed.Reason
		}
		return nil, &requestsFailure{reason, err.Error()}
	}
	return tracker, nil
}

// recordRequests sets status.requests to all, oldest first, keeping what a
// round recorded of the requests it took.
func (r *BriefReconciler) recordRequests(ctx context.Context, brief *c4v1beta1.Brief, all []model.Request) {
	status := &brief.Status
	before := map[string]c4v1beta1.BriefRequest{}
	for _, s := range status.Requests {
		before[requestName(s)] = s
	}
	listed, count := model.ListRequests(all)
	requests := make([]c4v1beta1.BriefRequest, 0, len(listed))
	for _, req := range listed {
		name := req.Name()
		prev, seen := before[name]
		if seen && req.Round > 0 {
			requests = append(requests, prev)
			continue
		}
		if !seen || prev.Stage != req.Stage {
			logf.FromContext(ctx).Info("A request changed stage", "request", name, "stage", req.Stage)
		}
		since := metav1.NewTime(req.Since)
		requests = append(requests, c4v1beta1.BriefRequest{Repository: req.Repository, Source: req.Source,
			Key: req.Key, Number: int32(req.Number), Author: req.Author, Title: req.Title, Body: req.Body, URL: req.URL,
			Stage: req.Stage, Round: int32(req.Round), Since: &since})
	}
	status.Requests = nil
	if len(requests) > 0 {
		status.Requests = requests
	}
	status.RequestsWaiting = int32(count)
}

// recordSettled keeps status.requestsSettled and
// status.requestSourcesSettled: each repository or source read now gets what
// its read found, one that couldn't be read keeps what it had, and one no
// longer read goes. settled is keyed by originID.
func recordSettled(brief *c4v1beta1.Brief, reading []requestOrigin, settled map[string][]int32) {
	before := map[string][]int32{}
	for _, s := range brief.Status.RequestsSettled {
		before[originID(s.Repository, "")] = s.Numbers
	}
	for _, s := range brief.Status.RequestSourcesSettled {
		before[originID("", s.Source)] = s.Numbers
	}
	var repositories []c4v1beta1.BriefRequestsSettled
	var sources []c4v1beta1.BriefRequestSourceSettled
	for _, origin := range reading {
		numbers, read := settled[origin.id()]
		if !read {
			numbers = before[origin.id()]
		}
		switch {
		case len(numbers) == 0:
		case origin.source != "":
			sources = append(sources, c4v1beta1.BriefRequestSourceSettled{Source: origin.source, Numbers: numbers})
		default:
			repositories = append(repositories, c4v1beta1.BriefRequestsSettled{Repository: origin.repository,
				Numbers: numbers})
		}
	}
	brief.Status.RequestsSettled, brief.Status.RequestSourcesSettled = repositories, sources
}

// setRequestsCondition says whether the repositories and sources that take
// requests are read, with a Warning Event when it goes False for a new
// reason. With none, the condition goes.
func (r *BriefReconciler) setRequestsCondition(ctx context.Context, brief *c4v1beta1.Brief,
	reading []requestOrigin, failure *requestsFailure) {
	status := &brief.Status
	if len(reading) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionRequests)
		return
	}
	if failure == nil {
		labels := make([]string, 0, len(reading))
		for _, origin := range reading {
			labels = append(labels, origin.label())
		}
		setCondition(&status.Conditions, brief.Generation, c4v1beta1.ConditionRequests, metav1.ConditionTrue,
			reasonRequestsReading, "Reading the issues of "+strings.Join(labels, ", "))
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
		req := model.Request{Repository: s.Repository, Source: s.Source, Key: s.Key, Number: int(s.Number),
			Author: s.Author, Title: s.Title, Body: s.Body, URL: s.URL, Stage: s.Stage, Round: int(s.Round),
			Issues: s.Issues, Release: s.Release, Why: s.Why}
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

// requestName names a request of status.requests as status.needsYou and
// status.roundRequests do.
func requestName(s c4v1beta1.BriefRequest) string {
	return model.Request{Repository: s.Repository, Source: s.Source, Key: s.Key, Number: int(s.Number)}.Name()
}

// knownOrigins are the repositories and sources of known requests, sorted by
// their originID.
func knownOrigins(known []model.Request) []requestOrigin {
	var out []requestOrigin
	for _, k := range known {
		o := requestOrigin{repository: k.Repository, source: k.Source}
		if !slices.ContainsFunc(out, func(x requestOrigin) bool { return x.id() == o.id() }) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b requestOrigin) int { return strings.Compare(a.id(), b.id()) })
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
